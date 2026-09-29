package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func freshHarness(t *testing.T) *harness {
	t.Helper()
	d := &recordingDriver{}
	s, secret, err := NewServer(t.TempDir(), "router", d, DemoDevice())
	if err != nil || secret != "" {
		t.Fatal("first run failed or generated a password")
	}
	return &harness{s: s, h: s.Handler(), driver: d}
}

func TestFirstSetupIsRequiredAndSurvivesRestart(t *testing.T) {
	h := freshHarness(t)
	if err := h.s.Resume(context.Background()); err != nil || h.driver.calls != 0 {
		t.Fatal("setup launched proxy")
	}
	for _, route := range []string{"/api/state", "/api/settings", "/api/subscription", "/api/password"} {
		if h.request("POST", route, `{}`, false).Code != 401 {
			t.Fatal("setup allowed access to " + route)
		}
	}
	login, _ := json.Marshal(map[string]string{"key": setupMarker})
	if h.request("POST", "/api/session", string(login), false).Code != 409 {
		t.Fatal("setup marker usable for login")
	}
	restarted, _, err := NewServer(h.s.dir, "router", h.driver, DemoDevice())
	if err != nil || !restarted.setupRequired {
		t.Fatal("closing the terminal lost setup state")
	}
	h.s, h.h = restarted, restarted.Handler()
	status := h.request("GET", "/api/setup", "", false)
	if strings.TrimSpace(status.Body.String()) != `{"required":true}` {
		t.Fatal("unexpected public setup status")
	}
	if w := h.request("POST", "/api/setup", passwordBody("new-login", "new-login"), false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.request("POST", "/api/setup", passwordBody("other-key", "other-key"), false).Code != 409 {
		t.Fatal("setup can overwrite established credential")
	}
	if h.request("GET", "/api/state", "", false).Code != 401 {
		t.Fatal("setting password removed login requirement")
	}
	if h.request("POST", "/api/session", `{"key":"new-login"}`, false).Code != 200 {
		t.Fatal("new password rejected")
	}
	restarted, _, err = NewServer(h.s.dir, "router", h.driver, DemoDevice())
	if err != nil || restarted.setupRequired {
		t.Fatal("restart reopened setup")
	}
	h.h = restarted.Handler()
	if h.request("POST", "/api/session", `{"key":"new-login"}`, false).Code != 200 {
		t.Fatal("password lost after restart")
	}
	// Losing an established credential must not silently reopen unauthenticated setup.
	if err := os.Remove(filepath.Join(h.s.dir, "admin.key")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewServer(h.s.dir, "router", h.driver, DemoDevice()); err == nil {
		t.Fatal("missing established credential reopened setup")
	}
}

func TestSetupValidationOriginAndFailedWrites(t *testing.T) {
	h := freshHarness(t)
	body := passwordBody("new-login", "new-login")
	r := httptest.NewRequest("POST", "/api/setup", strings.NewReader(body))
	r.Header.Set("Origin", "https://untrusted.invalid")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site setup allowed")
	}
	for _, body := range []string{passwordBody("12345678", "12345678"), passwordBody("new-login", "mismatch!"), passwordBody("routerlite-demo", "routerlite-demo")} {
		if h.request("POST", "/api/setup", body, false).Code != 400 {
			t.Fatal("invalid initial password accepted")
		}
	}
	h.s.Save = func(string, State) error { return errors.New("disk full") }
	if h.request("POST", "/api/setup", body, false).Code != 500 || !h.s.setupRequired {
		t.Fatal("state write failure finalized setup")
	}
	h.s.Save = SaveState
	h.s.WriteKey = func(string, []byte) error { return errors.New("disk full") }
	if h.request("POST", "/api/setup", body, false).Code != 500 || !h.s.setupRequired {
		t.Fatal("key write failure finalized setup")
	}
	restarted, _, err := NewServer(h.s.dir, "router", h.driver, DemoDevice())
	if err != nil || !restarted.setupRequired {
		t.Fatal("failed setup cannot be retried after restart")
	}
	h.h = restarted.Handler()
	if h.request("POST", "/api/setup", body, false).Code != 200 {
		t.Fatal("setup retry failed")
	}
}

func TestConcurrentSetupHasOneWinnerAndLegacyKeyPreserved(t *testing.T) {
	h := freshHarness(t)
	results := make(chan int, 2)
	var group sync.WaitGroup
	for _, password := range []string{"first-key", "other-key"} {
		group.Add(1)
		go func(password string) {
			defer group.Done()
			results <- h.request("POST", "/api/setup", passwordBody(password, password), false).Code
		}(password)
	}
	group.Wait()
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatal("concurrent setup has multiple winners", a, b)
	}
	legacy := newHarness(t)
	if legacy.s.setupRequired || legacy.request("POST", "/api/setup", passwordBody("other-key", "other-key"), false).Code != 409 {
		t.Fatal("existing installation reopened setup")
	}
	if legacy.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false).Code != 200 {
		t.Fatal("legacy password changed")
	}
}
