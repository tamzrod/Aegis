// internal/adapter/webui/auth.go
package webui

import (
	"crypto/subtle"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/tamzrod/Aegis/internal/config"
)

// basicAuth returns a middleware that enforces HTTP Basic Authentication.
// Authentication is always required; requests without valid credentials receive 401.
// cfg.PasswordHash must be a bcrypt hash of the password.
func basicAuth(cfg config.AuthConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		// Use constant-time comparison for username to resist timing attacks.
		// bcrypt.CompareHashAndPassword provides constant-time comparison for the password.
		usernameOK := ok && subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Username)) == 1
		passwordOK := ok && bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(pass)) == nil
		if !usernameOK || !passwordOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Aegis"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
