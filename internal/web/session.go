package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookie = "alfred_web_session"
	sessionTTL    = 12 * time.Hour
)

type Session struct {
	UserID      int64    `json:"uid"`
	DiscordID   string   `json:"did"`
	DisplayName string   `json:"name"`
	RoleIDs     []string `json:"roles"`
	ExpiresAt   int64    `json:"exp"`
}

func (s *Server) signSession(sess Session) (string, error) {
	if sess.ExpiresAt == 0 {
		sess.ExpiresAt = time.Now().Add(sessionTTL).Unix()
	}
	raw, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func (s *Server) parseSession(token string) (Session, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Session{}, fmt.Errorf("invalid session")
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return Session{}, fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return Session{}, err
	}
	if time.Now().Unix() > sess.ExpiresAt {
		return Session{}, fmt.Errorf("expired")
	}
	return sess, nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess Session) error {
	tok, err := s.signSession(sess)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.publicURL, "https://"),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *Server) sessionFromRequest(r *http.Request) (Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return Session{}, fmt.Errorf("no session")
	}
	return s.parseSession(c.Value)
}

const oauthStateTTL = 10 * time.Minute

// makeOAuthState returns a signed CSRF state (no cookie required — avoids
// localhost vs 127.0.0.1 and cross-site cookie drops on Discord redirect).
func (s *Server) makeOAuthState() (string, error) {
	nonce, err := randomState()
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("%s.%d", nonce, time.Now().Unix())
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func (s *Server) verifyOAuthState(state string) error {
	parts := strings.Split(state, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed")
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return fmt.Errorf("bad signature")
	}
	var ts int64
	if _, err := fmt.Sscanf(parts[1], "%d", &ts); err != nil || ts <= 0 {
		return fmt.Errorf("bad timestamp")
	}
	age := time.Now().Unix() - ts
	if age < -60 || age > int64(oauthStateTTL.Seconds()) {
		return fmt.Errorf("expired")
	}
	return nil
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
