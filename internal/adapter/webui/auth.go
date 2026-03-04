// internal/adapter/webui/auth.go
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tamzrod/Aegis/internal/config"
)

const (
	sessionCookieName = "aegis_session"
	sessionTTL        = 8 * time.Hour
	sessionTokenBytes = 32 // 256-bit random token
)

// sessionEntry holds the state for a single active session.
type sessionEntry struct {
	expiry                 time.Time
	passwordChangeRequired bool
}

// sessionStore is a thread-safe in-memory store of active session tokens.
// Sessions are not persisted; all active sessions are lost on server restart,
// requiring users to log in again.
type sessionStore struct {
	mu      sync.Mutex
	entries map[string]sessionEntry
}

func newSessionStore() *sessionStore {
	return &sessionStore{entries: make(map[string]sessionEntry)}
}

// create generates a cryptographically random session token, stores it with the
// given passwordChangeRequired flag, and returns the token.
func (s *sessionStore) create(passwordChangeRequired bool) (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.entries[token] = sessionEntry{
		expiry:                 time.Now().Add(sessionTTL),
		passwordChangeRequired: passwordChangeRequired,
	}
	s.mu.Unlock()
	return token, nil
}

// isValid reports whether token exists and has not expired.
func (s *sessionStore) isValid(token string) bool {
	s.mu.Lock()
	entry, ok := s.entries[token]
	s.mu.Unlock()
	return ok && time.Now().Before(entry.expiry)
}

// requiresPasswordChange reports whether the session requires a password change.
func (s *sessionStore) requiresPasswordChange(token string) bool {
	s.mu.Lock()
	entry, ok := s.entries[token]
	s.mu.Unlock()
	return ok && entry.passwordChangeRequired
}

// clearPasswordChangeRequired clears the passwordChangeRequired flag for a session.
func (s *sessionStore) clearPasswordChangeRequired(token string) {
	s.mu.Lock()
	if entry, ok := s.entries[token]; ok {
		s.entries[token] = sessionEntry{expiry: entry.expiry, passwordChangeRequired: false}
	}
	s.mu.Unlock()
}

// delete removes a session token.
func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

// requireSession is middleware that enforces session-cookie authentication.
// Requests without a valid session cookie are redirected to /login.
// If the session has passwordChangeRequired set, all requests except
// /change-password, /api/change-password, and /api/logout are redirected
// to /change-password.
func requireSession(store *sessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !store.isValid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		// If a password change is required, restrict access to the change-password routes.
		if store.requiresPasswordChange(cookie.Value) {
			p := r.URL.Path
			if p != "/change-password" && p != "/api/change-password" && p != "/api/logout" {
				http.Redirect(w, r, "/change-password", http.StatusFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// checkCredentials returns true when the supplied username and password match cfg.
// cfg.PasswordHash must be a bcrypt hash of the password.
func checkCredentials(cfg config.AuthConfig, username, password string) bool {
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Username)) == 1
	passwordOK := bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(password)) == nil
	return usernameOK && passwordOK
}
