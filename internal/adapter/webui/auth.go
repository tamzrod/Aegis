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

// sessionStore is a thread-safe in-memory store of active session tokens.
// Sessions are not persisted; all active sessions are lost on server restart,
// requiring users to log in again.
type sessionStore struct {
	mu      sync.Mutex
	entries map[string]time.Time // token → expiry
}

func newSessionStore() *sessionStore {
	return &sessionStore{entries: make(map[string]time.Time)}
}

// create generates a cryptographically random session token, stores it, and returns it.
func (s *sessionStore) create() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.entries[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return token, nil
}

// isValid reports whether token exists and has not expired.
func (s *sessionStore) isValid(token string) bool {
	s.mu.Lock()
	exp, ok := s.entries[token]
	s.mu.Unlock()
	return ok && time.Now().Before(exp)
}

// delete removes a session token.
func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

// requireSession is middleware that enforces session-cookie authentication.
// Requests without a valid session cookie are redirected to /login.
func requireSession(store *sessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !store.isValid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
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
