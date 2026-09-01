// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"latere.ai/x/pkg/oidc"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// sessionTTL is how long a signed-in session lasts before a re-login.
const sessionTTL = 12 * time.Hour

// OIDCClient is the slice of oidc.Provider the login uses. Behind an
// interface so the callback flow tests offline against a fake; the real
// *oidc.Provider satisfies it.
type OIDCClient interface {
	AuthCodeURL(state, nonce, verifier string, extra ...oauth2.AuthCodeOption) string
	Exchange(ctx context.Context, code, verifier string) (*oauth2.Token, error)
	VerifyIDToken(ctx context.Context, tok *oauth2.Token, nonce string) (oidc.Identity, error)
}

// Profile is what /userinfo yields: the email the gate binds a grant on, and
// the two display claims the shell renders (006m). Nothing else is decoded.
type Profile struct {
	Email   string
	Name    string
	Picture string
}

// UserInfoFunc fetches the profile from the OIDC userinfo endpoint with an
// access token. latere auth carries the sub in the ID token but the email (PII)
// only at /userinfo, so the login fetches it there when the ID token has none.
// The access token is discarded with the rest of the callback, so this is the
// one moment the profile can be read at all. Behind a function so it tests
// offline.
type UserInfoFunc func(ctx context.Context, accessToken string) (Profile, error)

// Login is the OIDC login front door: a standard public PKCE flow, sealed
// session and flow cookies, and the middleware that turns a session into an
// httpapi.Identity. It consumes only sub + verified email from the ID token —
// roles, scopes, and org claims are dropped (006c AC1).
type Login struct {
	auth    OIDCClient
	codec   *Codec
	access  access.Access
	members access.Membership // optional; nil skips the membership bind
	// endSession is the issuer's RP-initiated logout. Empty leaves sign-out
	// local-only, which is what an issuer advertising none can offer.
	endSession string
	baseURL    string
	secure     bool // __Host- cookies require Secure; false only for insecure dev
	userInfo   UserInfoFunc
}

// NewLogin builds the login front door. userInfo may be nil, in which case the
// email is taken only from the ID token.
func NewLogin(auth OIDCClient, codec *Codec, acc access.Access, baseURL string, secure bool, userInfo UserInfoFunc) *Login {
	return &Login{auth: auth, codec: codec, access: acc, baseURL: strings.TrimRight(baseURL, "/"), secure: secure, userInfo: userInfo}
}

// EndSessionAt tells the login where the issuer ends its own session, so
// signing out here signs out there too. Returned fluently, like BindMemberships.
func (l *Login) EndSessionAt(url string) *Login {
	l.endSession = url
	return l
}

// BindMemberships gives the login the membership store to bind on sign-in.
// Optional, and returned fluently so the wiring reads as one expression: a
// deployment without a membership store simply never binds.
func (l *Login) BindMemberships(m access.Membership) *Login {
	l.members = m
	return l
}

// flowState is the per-login secret carried in the flow cookie across the
// redirect: the CSRF state, the ID-token nonce, and the PKCE verifier.
type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"r"`
}

// HandleLogin starts the flow: mint state/nonce/verifier, seal them into the
// flow cookie, and redirect to the issuer.
func (l *Login) HandleLogin(w http.ResponseWriter, r *http.Request) {
	fs := flowState{
		State:    randToken(),
		Nonce:    randToken(),
		Verifier: oidc.GenerateVerifier(),
		Next:     safeNext(r.URL.Query().Get("next")),
	}
	sealed, err := l.codec.Seal(fs)
	if err != nil {
		http.Error(w, "login init failed", http.StatusInternalServerError)
		return
	}
	setCookie(w, flowCookie, sealed, l.secure, 10*time.Minute)
	http.Redirect(w, r, l.auth.AuthCodeURL(fs.State, fs.Nonce, fs.Verifier), http.StatusFound)
}

// HandleCallback completes the flow: verify the state against the flow cookie,
// exchange the code, verify the ID token, and set the session cookie. The flow
// cookie is deleted before the exchange so a replayed callback cannot reuse it.
func (l *Login) HandleCallback(w http.ResponseWriter, r *http.Request) {
	raw, ok := readCookie(r, flowCookie, l.secure)
	if !ok {
		http.Error(w, "no login in progress", http.StatusBadRequest)
		return
	}
	clearCookie(w, flowCookie, l.secure)

	var fs flowState
	if err := l.codec.Open(raw, &fs); err != nil {
		http.Error(w, "bad login state", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(fs.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	tok, err := l.auth.Exchange(r.Context(), code, fs.Verifier)
	if err != nil {
		http.Error(w, "code exchange failed", http.StatusBadGateway)
		return
	}
	id, err := l.auth.VerifyIDToken(r.Context(), tok, fs.Nonce)
	if err != nil {
		http.Error(w, "invalid id token", http.StatusUnauthorized)
		return
	}

	sess := sessionFromIdentity(id)
	// The sub comes from the ID token; the email may live only at /userinfo
	// (latere auth keeps PII out of the token), and the display claims live only
	// there. This is the only moment an access token exists, so the profile is
	// read once here and carried in the session (006m) — a failure costs the name
	// and avatar, never the sign-in.
	if l.userInfo != nil {
		if p, err := l.userInfo(r.Context(), tok.AccessToken); err == nil {
			if sess.Email == "" && p.Email != "" {
				sess.Email = p.Email
			}
			sess.Name, sess.Picture = p.Name, safePicture(p.Picture)
		}
	}

	// Signing in admits you (006q). Anyone who proves their identity gets
	// TierUser; what limits them is budget, metered inside Enqueue, not an
	// admin's guest list. The gate is untouched and still fail-closed — this
	// only creates the row it then finds — and SelfGrant never promotes, never
	// demotes, and never re-admits a revoked address, so a seeded admin stays an
	// admin through their first login and a ban survives one.
	if _, _, err := l.access.SelfGrant(r.Context(), access.Principal{Sub: sess.Sub, Email: sess.Email}); err != nil {
		log.Printf("login: admitting %s: %v", sess.Sub, err)
	}

	// Sign-in is the one moment that always happens, so it is where a project
	// invitation stops being an email and becomes this principal's membership.
	// It cannot live in an authorisation check: every one of those short-circuits
	// for an owner or a public project, leaving an invitee unbound — absent from
	// "shared with me" and permanently "pending" in the members view. Best-effort
	// and logged: a bind failure costs a collaborator one sign-in, never the
	// sign-in itself, and the next one retries it.
	if l.members != nil && sess.Email != "" {
		if n, err := l.members.BindAll(r.Context(), access.Principal{Sub: sess.Sub, Email: sess.Email}); err != nil {
			log.Printf("login: binding memberships for %s: %v", sess.Sub, err)
		} else if n > 0 {
			log.Printf("login: bound %d project membership(s) for %s", n, sess.Sub)
		}
	}

	sealed, err := l.codec.Seal(sess)
	if err != nil {
		http.Error(w, "session init failed", http.StatusInternalServerError)
		return
	}
	setCookie(w, sessionCookie, sealed, l.secure, sessionTTL)
	http.Redirect(w, r, l.baseURL+fs.Next, http.StatusFound)
}

// HandleLogout ends the session here *and* at the identity provider.
//
// Clearing only this app's cookie is not signing out. The provider's own
// session survives it, so the next visit to /login is answered with a fresh
// code for the same account without ever showing a prompt: the app looks signed
// out, bounces straight back in, and there is no way to reach another profile
// or to actually leave a shared machine.
//
// So the local cookie goes first — it must be gone whatever the provider does
// next — and the browser is then handed to the issuer's RP-initiated logout,
// which returns it here. An issuer that advertises no such endpoint leaves
// sign-out local-only, which is all it can offer.
func (l *Login) HandleLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, sessionCookie, l.secure)
	http.Redirect(w, r, l.logoutURL(), http.StatusFound)
}

// logoutURL is where the browser goes after the local cookie is cleared.
func (l *Login) logoutURL() string {
	home := l.baseURL + "/"
	if l.endSession == "" {
		return home
	}
	u, err := url.Parse(l.endSession)
	if err != nil {
		return home
	}
	q := u.Query()
	q.Set("post_logout_redirect_uri", home)
	u.RawQuery = q.Encode()
	return u.String()
}

// Middleware reads the session cookie and places the identity on the request so
// the gate can resolve a grant. A request with no valid session passes through
// unauthenticated; the gate refuses it.
func (l *Login) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := readCookie(r, sessionCookie, l.secure); ok {
			var s Session
			if err := l.codec.Open(raw, &s); err == nil && s.Sub != "" {
				r = r.WithContext(httpapi.WithIdentity(r.Context(), httpapi.Identity{
					Sub:     s.Sub,
					Email:   s.Email,
					Name:    s.Name,
					Picture: s.Picture,
				}))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Credits is what a principal has to spend, as /me reports it (011). The wallet
// is rendered beside the "New project" button, and the reserve is what the
// client needs to say *this project needs at least this much to start a
// session* before a submit is attempted.
type Credits struct {
	// Wallet is this person's own balance, in micro-USD. It is never spent by a
	// session directly: money reaches a run by being allocated to a project.
	Wallet int64 `json:"wallet"`
	// StartReserve is how much a project must have available before a session
	// may start there. Public on purpose — the refusal it causes has to be
	// explainable before it happens.
	StartReserve int64 `json:"start_reserve"`
}

// CreditsFunc reports one principal's credits. Injected so this package does
// not import the ledger or the policy store; nil leaves the field off /me
// entirely, which is what an offline unmetered run wants.
type CreditsFunc func(ctx context.Context, principalSub, email string) (Credits, error)

// FrozenFunc reports whether an account is in view-only mode (022d). Injected
// for the same reason CreditsFunc is; nil leaves the field off /me, so the
// frontend renders no banner when this deployment sells no credits.
type FrozenFunc func(ctx context.Context, email string) (bool, error)

// Me reports the current identity and its resolved tier. It sits OUTSIDE the
// gate and always answers 200: an ungranted principal gets tier "none", which is
// how the frontend learns to render the not-invited screen.
func Me(acc access.Access, credits CreditsFunc, frozen FrozenFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpapi.IdentityFrom(r)
		if !ok || id.Sub == "" {
			writeJSON(w, map[string]any{"authenticated": false})
			return
		}
		tier, _, _ := acc.Resolve(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email})
		body := map[string]any{
			"authenticated": true,
			"principal":     id.Sub,
			"email":         id.Email,
			"tier":          tier.String(),
		}
		// Display claims are reported for every authenticated caller, ungranted
		// ones included, so the not-invited screen can greet a person (006m).
		if id.Name != "" {
			body["name"] = id.Name
		}
		if id.Picture != "" {
			body["picture"] = id.Picture
		}
		// Credits are reported only for a granted principal: an ungranted one
		// has nothing to spend, and the not-invited screen has nothing to show.
		if credits != nil && tier.Granted() {
			if c, err := credits(r.Context(), id.Sub, id.Email); err == nil {
				body["credits"] = c
			} else {
				log.Printf("me: reading credits for %s: %v", id.Sub, err)
			}
		}
		// Frozen rides along so the frontend can render the view-only banner and
		// disable the spend controls, over the same predicate the gate enforces.
		if frozen != nil && tier.Granted() {
			if f, err := frozen(r.Context(), id.Email); err == nil {
				body["frozen"] = f
			} else {
				log.Printf("me: reading frozen for %s: %v", id.Sub, err)
			}
		}
		writeJSON(w, body)
	}
}

// sessionFromIdentity maps the verified ID token to the session, keeping only
// sub and email. Roles and every other claim are dropped — no IdP-issued
// authority may reach a grant decision (006c AC1). The display claims are added
// afterwards, from /userinfo, and confer nothing.
func sessionFromIdentity(id oidc.Identity) Session {
	return Session{Sub: id.Subject, Email: id.Email}
}

// safePicture keeps an avatar URL only when it is an absolute https URL. A
// javascript: or data: value from the issuer must never reach an <img src>, and
// an unusable value degrades to initials rather than a broken image (006m).
func safePicture(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return u.String()
}

// safeNext allows only a same-site relative path, so ?next= cannot become an
// open redirect to another host.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return "/"
}

// randToken returns a URL-safe 128-bit random string.
func randToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// writeJSON writes v as a 200 JSON response. Every answer this package sends
// through it is a 200 — the failures go out as plain text through http.Error —
// so the status is not a parameter: one would only ever be passed
// http.StatusOK, and a caller reading it might believe otherwise.
//
// The value is encoded before the status line is committed, so a body that
// cannot become JSON answers 500 rather than a 200 whose payload is empty and
// whose caller cannot tell.
func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("writeJSON: encoding a %T response: %v", v, err)
		http.Error(w, "response encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
