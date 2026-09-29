package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func passwordBody(password, confirm string) string {
	b, _ := json.Marshal(map[string]string{"password": password, "confirm": confirm})
	return string(b)
}

func TestPasswordRotationPersistsAndRevokesAllSessions(t *testing.T) {
	h := newHarness(t)
	h.success(t, "/api/subscription", `{"url":"demo://starter"}`)
	h.success(t, "/api/settings", `{"enabled":true}`)
	before, _ := json.Marshal(h.s.state)
	calls := h.driver.calls
	second := h.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false).Result().Cookies()[0]
	password := "test-only-new-password"
	w := h.request("POST", "/api/password", passwordBody(password, password), true)
	if w.Code != 200 || strings.Contains(w.Body.String(), password) {
		t.Fatal("rotation failed or leaked credential")
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("current session cookie not expired")
	}
	for _, cookie := range []*http.Cookie{h.cookie, second} {
		h.cookie = cookie
		if h.request("GET", "/api/state", "", true).Code != 401 || h.request("POST", "/api/password", passwordBody(password, password), true).Code != 401 {
			t.Fatal("old session can still read or rotate password")
		}
	}
	if h.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false).Code != 401 {
		t.Fatal("old password still accepted")
	}
	login, _ := json.Marshal(map[string]string{"key": password})
	w = h.request("POST", "/api/session", string(login), false)
	if w.Code != 200 || strings.Contains(w.Body.String(), password) {
		t.Fatal("new login failed or secret leaked")
	}
	after, _ := json.Marshal(h.s.state)
	if string(before) != string(after) || h.driver.calls != calls || !h.driver.Running() {
		t.Fatal("password change disturbed proxy")
	}
	stored, err := os.ReadFile(filepath.Join(h.s.dir, "admin.key"))
	if err != nil || string(stored) != password {
		t.Fatal("credential not persisted")
	}
	restarted, _, err := NewServer(h.s.dir, "demo", &DemoDriver{}, DemoDevice())
	if err != nil {
		t.Fatal(err)
	}
	h.h = restarted.Handler()
	if h.request("POST", "/api/session", string(login), false).Code != 200 {
		t.Fatal("new password lost after restart")
	}
}

func TestPasswordAuthorizationValidationAndWriteFailure(t *testing.T) {
	h := newHarness(t)
	valid := passwordBody("test-only-new-password", "test-only-new-password")
	if h.request("POST", "/api/password", valid, false).Code != 401 {
		t.Fatal("unauthenticated rotation allowed")
	}
	r := httptest.NewRequest("POST", "/api/password", strings.NewReader(valid))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.invalid")
	r.AddCookie(h.cookie)
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin rotation allowed")
	}
	for _, pair := range [][2]string{
		{"short", "short"},
		{strings.Repeat("x", 129), strings.Repeat("x", 129)},
		{"test-only-password", "different-password"},
		{" test-only-password", " test-only-password"},
		{"test-only-password\n", "test-only-password\n"},
		{"test\x00only-password", "test\x00only-password"},
	} {
		if h.request("POST", "/api/password", passwordBody(pair[0], pair[1]), true).Code != 400 {
			t.Fatal("invalid password accepted")
		}
	}
	if h.request("POST", "/api/password", passwordBody(strings.Repeat("x", 5000), "x"), true).Code != 400 {
		t.Fatal("oversized body accepted")
	}
	h.s.mode = "router"
	if h.request("POST", "/api/password", passwordBody("routerlite-demo", "routerlite-demo"), true).Code != 400 {
		t.Fatal("router accepted public demo credential")
	}
	h.s.mode = "demo"
	h.s.WriteKey = func(string, []byte) error { return errors.New("disk full") }
	if h.request("POST", "/api/password", valid, true).Code != 500 {
		t.Fatal("write failure reported success")
	}
	if h.request("GET", "/api/state", "", true).Code != 200 || h.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false).Code != 200 {
		t.Fatal("failed write invalidated old credentials/session")
	}
	stored, _ := os.ReadFile(filepath.Join(h.s.dir, "admin.key"))
	if string(stored) != "routerlite-demo" {
		t.Fatal("failed write altered stored credential")
	}
}
