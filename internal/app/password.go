package app

import (
	"crypto/sha256"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MinPasswordLength = 9

// Contains control characters, so it can never be chosen as a valid password.
// An atomic replacement of this marker completes setup even across restarts.
const setupMarker = "\x00routerlite-setup-v1\x00"

// The authenticated session is the authorization to change the password.
// Handler holds s.mu, serializing rotation with login and other management calls.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	s.setPassword(w, r, false)
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, initial bool) {
	var body struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !readBody(w, r, &body) {
		return
	}
	length := utf8.RuneCountInString(body.Password)
	if length < MinPasswordLength || length > 128 || strings.TrimSpace(body.Password) != body.Password || strings.IndexFunc(body.Password, unicode.IsControl) >= 0 {
		fail(w, 400, "新密码需为 9–128 个字符，不能以空白开头或结尾，也不能包含控制字符")
		return
	}
	if body.Password != body.Confirm {
		fail(w, 400, "两次输入的新密码不一致")
		return
	}
	if s.mode == "router" && body.Password == "routerlite-demo" {
		fail(w, 400, "请勿使用公开的演示口令")
		return
	}
	if initial {
		// A missing credential beside persisted state must fail closed on restart.
		// Save before replacing the setup marker; failed writes remain retryable.
		if err := s.Save(s.dir, s.state); err != nil {
			fail(w, 500, "初始化保存失败，请检查可用存储后重试")
			return
		}
	}
	// Persist first. A failed write must leave both the old key and sessions usable.
	if err := s.WriteKey(filepath.Join(s.dir, "admin.key"), []byte(body.Password)); err != nil {
		if initial {
			fail(w, 500, "密码保存失败，请检查可用存储后重新设置")
		} else {
			fail(w, 500, "密码保存失败，原密码仍然有效，请检查可用存储")
		}
		return
	}
	s.key = sha256.Sum256([]byte(body.Password))
	s.setupRequired = false
	s.sessions = map[string]time.Time{}
	s.failures = 0
	s.blockedUntil = time.Time{}
	s.event("管理密码已修改，所有管理会话已退出")
	http.SetCookie(w, &http.Cookie{Name: "rpl_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
