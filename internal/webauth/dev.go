package webauth

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/latere-ai/replichai/internal/httpapi"
)

// DevMiddleware attributes every request to a synthetic principal, so the app
// runs locally without an identity provider. It authenticates but never grants:
// operating still needs a grant an admin seeded (e.g. via ADMIN_EMAILS), so the
// dev bypass is not a back door onto the shared credential (006c AC6).
func DevMiddleware(sub, email string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(httpapi.WithIdentity(r.Context(), httpapi.Identity{
				Sub:   sub,
				Email: email,
			})))
		})
	}
}

// CheckDevBypass refuses the dev bypass on a deployment that is not loopback,
// unless an explicit insecure override is set. A public listen address with the
// bypass on would admit every request as the synthetic principal, so a
// non-loopback bind fails closed rather than shipping the back door (AC6). The
// override exists for a containerised local run that binds all interfaces
// (compose sets it); it must never be set in production.
func CheckDevBypass(addr string, insecureOverride bool) error {
	if insecureOverride || isLoopbackAddr(addr) {
		return nil
	}
	return fmt.Errorf("dev bypass refused on non-loopback address %q: bind a loopback address or set DEV_INSECURE=true for a local container", addr)
}

// isLoopbackAddr reports whether a listen address binds only the loopback
// interface. An empty or all-interfaces host is reachable, so it is not
// loopback.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return false // all interfaces — reachable
	case "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
