package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

const csrfCookie = "isucon_csrf"

func (s *server) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	token := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		HttpOnly: false,
	})
	return token
}

func (s *server) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if !strings.HasPrefix(host, "127.0.0.1:") && host != "127.0.0.1" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "待受ホストが不正です"})
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "http://"+host {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Origin が不正です"})
		return false
	}
	cookie, err := r.Cookie(csrfCookie)
	header := r.Header.Get("X-CSRF-Token")
	if err != nil || cookie.Value == "" || header == "" || header != cookie.Value {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "CSRF トークンが不正です"})
		return false
	}
	return true
}
