package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SessionCookie — имя cookie веб-сессии.
const SessionCookie = "mkey_session"

// secure оборачивает обработчик TCP-транспорта проверками SEC-5:
// Host (защита от DNS rebinding), Origin для изменяющих запросов (CSRF), токен для /api/.
func (m *Module) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Host — только наш адрес: страница чужого сайта, «перепривязанная» на 127.0.0.1, не пройдёт.
		if !m.allowedHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		// Изменяющие запросы из браузера — только со страниц самого mKey.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && !m.allowedOrigin(origin) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}

		// Вход по ссылке /?t=<токен>: ставим cookie сессии и убираем токен из адреса.
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
			if !m.validToken(t) {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: SessionCookie, Value: m.token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(30 * 24 * time.Hour),
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		// API — только с токеном (заголовок или cookie). Статические файлы GUI открыты.
		if strings.HasPrefix(r.URL.Path, "/api/") && !m.authorized(r) {
			m.writeError(w, r, http.StatusUnauthorized, "api.unauthorized", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHost сообщает, что заголовок Host — 127.0.0.1:<порт> или localhost:<порт>.
func (m *Module) allowedHost(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != strconv.Itoa(m.port) {
		return false
	}
	return h == "127.0.0.1" || h == "localhost"
}

// allowedOrigin сообщает, что Origin — страница самого mKey.
func (m *Module) allowedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "http" && m.allowedHost(u.Host)
}

// authorized проверяет токен в заголовке Authorization или в cookie сессии.
func (m *Module) authorized(r *http.Request) bool {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && m.validToken(t) {
		return true
	}
	c, err := r.Cookie(SessionCookie)
	return err == nil && m.validToken(c.Value)
}

// validToken сравнивает токен за постоянное время (не выдаёт совпадение по времени ответа).
func (m *Module) validToken(t string) bool {
	return m.token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(m.token)) == 1
}
