package webauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"latere.ai/x/pkg/oidclogin"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// sessionTTL is how long a signed-in session lasts before a re-login.
const sessionTTL = 12 * time.Hour

// OIDCClient is the slice of oidclogin.Authenticator the login uses. Behind an
// interface so the callback flow tests offline against a fake; the real
// *oidclogin.Authenticator satisfies it.
type OIDCClient interface {
	AuthCodeURL(state, nonce, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (*oauth2.Token, error)
	VerifyIDToken(ctx context.Context, tok *oauth2.Token, nonce string) (oidclogin.Identity, error)
}

// UserInfoFunc fetches the email and its verification from the OIDC userinfo
// endpoint with an access token. Some issuers (latere auth) carry the verified
// sub in the ID token but the email only at /userinfo, so the login fetches it
// there when the ID token has none. Behind a function so it tests offline.
type UserInfoFunc func(ctx context.Context, accessToken string) (email string, verified bool, err error)

// Login is the OIDC login front door: a standard public PKCE flow, sealed
// session and flow cookies, and the middleware that turns a session into an
// httpapi.Identity. It consumes only sub + verified email from the ID token —
// roles, scopes, and org claims are dropped (006c AC1).
type Login struct {
	auth     OIDCClient
	codec    *Codec
	access   access.Access
	baseURL  string
	secure   bool // __Host- cookies require Secure; false only for insecure dev
	userInfo UserInfoFunc
}

// NewLogin builds the login front door. userInfo may be nil, in which case the
// email is taken only from the ID token.
func NewLogin(auth OIDCClient, codec *Codec, acc access.Access, baseURL string, secure bool, userInfo UserInfoFunc) *Login {
	return &Login{auth: auth, codec: codec, access: acc, baseURL: strings.TrimRight(baseURL, "/"), secure: secure, userInfo: userInfo}
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
		Verifier: oidclogin.GenerateVerifier(),
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
	// The verified sub comes from the ID token; the email may only be at
	// /userinfo (latere auth). Fetch it there when the ID token carried none.
	if sess.Email == "" && l.userInfo != nil {
		if email, verified, err := l.userInfo(r.Context(), tok.AccessToken); err == nil && email != "" {
			sess.Email = email
			sess.EmailVerified = verified
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

// HandleLogout clears the session and returns to the site root.
func (l *Login) HandleLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, sessionCookie, l.secure)
	http.Redirect(w, r, l.baseURL+"/", http.StatusFound)
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
					Sub:           s.Sub,
					Email:         s.Email,
					EmailVerified: s.EmailVerified,
				}))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Me reports the current identity and its resolved tier. It sits OUTSIDE the
// gate and always answers 200: an ungranted principal gets tier "none", which is
// how the frontend learns to render the not-invited screen.
func Me(acc access.Access) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpapi.IdentityFrom(r)
		if !ok || id.Sub == "" {
			writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
			return
		}
		tier, _, _ := acc.Resolve(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email, EmailVerified: id.EmailVerified})
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":   true,
			"principal":       id.Sub,
			"email":           id.Email,
			"tier":            tier.String(),
			"linked_accounts": map[string]string{}, // the forge link is 006f
		})
	}
}

// sessionFromIdentity maps a verified ID token to the session, keeping only sub
// and the verified email. Roles, name, and every other claim are dropped — no
// IDP-issued authority may reach a grant decision (006c AC1).
func sessionFromIdentity(id oidclogin.Identity) Session {
	return Session{
		Sub:           id.Subject,
		Email:         id.Email,
		EmailVerified: emailVerified(id.Raw),
	}
}

// emailVerified reads the ID token's email_verified claim.
func emailVerified(raw map[string]any) bool { return verifiedFromAny(raw["email_verified"]) }

// verifiedFromAny reads an email_verified value as either a bool or the string
// "true"; anything else is false, so a mis-typed claim never admits the wrong
// person.
func verifiedFromAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	default:
		return false
	}
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

// writeJSON writes v as a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
