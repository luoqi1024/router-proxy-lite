package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionParsing(t *testing.T) {
	nodes, _, err := ParseSubscription(DemoSubscription())
	if err != nil || len(nodes) != 4 {
		t.Fatalf("%v, %d", err, len(nodes))
	}
	updated := bytes.ReplaceAll(DemoSubscription(), []byte("美国 · 西海岸 12"), []byte("renamed"))
	renamed, _, _ := ParseSubscription(updated)
	if renamed[0].ID != nodes[0].ID {
		t.Fatal("rename changed identity")
	}
	duplicate := []byte("proxies:\n - {name: a, type: anytls, server: x.invalid, port: 443, password: one}\n - {name: b, type: anytls, server: x.invalid, port: 443, password: two}\n - {name: unsupported, type: hysteria2, server: x.invalid, port: 443, password: three}\n")
	got, warnings, err := ParseSubscription(duplicate)
	if err != nil || len(got) != 2 || len(warnings) != 1 {
		t.Fatalf("accounts lost: %v %v", got, warnings)
	}
	for _, bad := range [][]byte{[]byte("not yaml:"), []byte("proxies: ["), bytes.Repeat([]byte("x"), MaxSubscriptionBytes+1), []byte("proxies: [{name: a, type: anytls, server: x.invalid, port: 443, password: a, network: ws}]")} {
		if _, _, err := ParseSubscription(bad); err == nil {
			t.Fatal("invalid subscription accepted")
		}
	}
	many := "proxies:\n" + strings.Repeat(" - {name: a, type: anytls, server: x.invalid, port: 443, password: a}\n", MaxNodes+1)
	if _, _, err := ParseSubscription([]byte(many)); err == nil {
		t.Fatal("node limit ignored")
	}
}

func TestSubscriptionAddressGuard(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "198.18.0.1", "::1", "::ffff:127.0.0.1", "fd00::1", "fe80::1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	for _, raw := range []string{"http://public.example/sub", "https://127.0.0.1/sub", "https://[::1]/", "https://user:pass@example.com/", "https://example.com/#fragment"} {
		if _, err := validateURL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := validateURL("https://provider.example/sub?token=demo-only"); err != nil {
		t.Fatal(err)
	}
}

type recordingDriver struct {
	state State
	fail  bool
	calls int
}

func (d *recordingDriver) Apply(_ context.Context, s State) error {
	d.calls++
	if d.fail {
		return errors.New("simulated failure")
	}
	d.state = s
	return nil
}
func (d *recordingDriver) Running() bool { return d.state.Enabled && d.state.Policy != "direct" }
func (d *recordingDriver) Close() error  { return nil }

type harness struct {
	s      *Server
	h      http.Handler
	cookie *http.Cookie
	driver *recordingDriver
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d := &recordingDriver{}
	s, _, err := NewServer(t.TempDir(), "demo", d, DemoDevice())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{s: s, h: s.Handler(), driver: d}
	w := h.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	h.cookie = w.Result().Cookies()[0]
	return h
}
func (h *harness) request(method, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.AddCookie(h.cookie)
	}
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	return w
}
func (h *harness) success(t *testing.T, path, body string) View {
	t.Helper()
	w := h.request("POST", path, body, true)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
	}
	var v View
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAPIWorkflowAndSecretRedaction(t *testing.T) {
	h := newHarness(t)
	if h.request("GET", "/api/state", "", false).Code != 401 {
		t.Fatal("unauthenticated status exposed")
	}
	h.s.Fetch = func(context.Context, string) ([]byte, error) { return DemoSubscription(), nil }
	v := h.success(t, "/api/subscription", `{"url":"https://provider.example/?token=private-demo-token","name":"Test"}`)
	if len(v.Nodes) != 4 || v.Enabled {
		t.Fatalf("bad import: %+v", v)
	}
	v = h.success(t, "/api/settings", `{"selected":"`+v.Nodes[1].ID+`","enabled":true}`)
	if !v.Running {
		t.Fatal("not running")
	}
	h.success(t, "/api/settings", `{"policy":"global"}`)
	v = h.success(t, "/api/settings", `{"policy":"direct"}`)
	if v.Running {
		t.Fatal("direct must stop core")
	}
	v = h.success(t, "/api/settings", `{"policy":"rule"}`)
	if !v.Running {
		t.Fatal("rule should restart")
	}
	before := h.s.state
	for _, body := range []string{`{"selected":"missing"}`, `{"policy":"typo"}`, `{"enabled":true,"surprise":1}`, `{} {}`} {
		if h.request("POST", "/api/settings", body, true).Code == 200 {
			t.Fatal("bad setting accepted")
		}
	}
	if h.s.state.Selected != before.Selected || h.s.state.Policy != before.Policy {
		t.Fatal("invalid request changed state")
	}
	response := h.request("GET", "/api/state", "", true).Body.String()
	for _, secret := range []string{"private-demo-token", "demo-only", "us12.example.invalid", "outbound", "provider.example"} {
		if strings.Contains(response, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	// Refresh removing/changing a selected endpoint disables instead of silently using another exit.
	h.s.Fetch = func(context.Context, string) ([]byte, error) {
		return []byte("proxies: [{name: replacement, type: anytls, server: new.invalid, port: 443, password: demo}]"), nil
	}
	v = h.success(t, "/api/subscription/refresh", `{}`)
	if v.Enabled || v.Running || len(v.Nodes) != 1 {
		t.Fatal("replacement must need explicit enable")
	}
	persisted, err := LoadState(h.s.dir)
	if err != nil || persisted.Selected != v.Selected {
		t.Fatal("state did not persist", err)
	}
	if h.request("DELETE", "/api/session", "", true).Code != 200 || h.request("GET", "/api/state", "", true).Code != 401 {
		t.Fatal("logout failed")
	}
}

func TestTransactionsRestoreAfterFailure(t *testing.T) {
	h := newHarness(t)
	h.success(t, "/api/subscription", `{"url":"demo://starter"}`)
	h.success(t, "/api/settings", `{"enabled":true}`)
	h.driver.fail = true
	if h.request("POST", "/api/settings", `{"policy":"global"}`, true).Code != 409 || h.s.state.Policy != "rule" {
		t.Fatal("driver failure changed state")
	}
	h.driver.fail = false
	h.s.Save = func(string, State) error { return errors.New("disk full") }
	if h.request("POST", "/api/settings", `{"enabled":false}`, true).Code != 409 {
		t.Fatal("save failure ignored")
	}
	if !h.driver.Running() || !h.s.state.Enabled {
		t.Fatal("original runtime was not restored")
	}
	h.s.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("download failed") }
	if h.request("POST", "/api/subscription", `{"url":"https://provider.example/sub"}`, true).Code != 400 || !h.s.state.Enabled {
		t.Fatal("failed download changed state")
	}
}

func TestSessionAndCSRF(t *testing.T) {
	h := newHarness(t)
	if !h.cookie.HttpOnly || h.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure session cookie")
	}
	r := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"enabled":false}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.invalid")
	r.AddCookie(h.cookie)
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	for i := 0; i < 8; i++ {
		if h.request("POST", "/api/session", `{"key":"wrong"}`, false).Code != 401 {
			t.Fatal("incorrect key accepted")
		}
	}
	if h.request("POST", "/api/session", `{"key":"routerlite-demo"}`, false).Code != 429 {
		t.Fatal("login rate limit missing")
	}
}

func TestConfigAndOptionalRealCoreCheck(t *testing.T) {
	nodes, _, _ := ParseSubscription(DemoSubscription())
	extra, _, err := ParseSubscription([]byte("proxies:\n - {name: vless, type: vless, server: vless.example.invalid, port: 443, uuid: 00000000-0000-4000-8000-000000000003, tls: true}\n - {name: websocket, type: vmess, server: ws.example.invalid, port: 443, uuid: 00000000-0000-4000-8000-000000000004, tls: true, network: ws, ws-opts: {path: /example, headers: {Host: ws.example.invalid}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, extra...)
	s := DefaultState()
	s.Enabled = true
	s.Subscription.Nodes = nodes
	assets := os.Getenv("RPL_TEST_ASSETS")
	if assets == "" {
		assets = t.TempDir()
	}
	for _, node := range nodes {
		for _, policy := range []string{"rule", "global"} {
			s.Selected = node.ID
			s.Policy = policy
			b, err := RenderConfig(s, DemoDevice(), assets)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if json.Unmarshal(b, &config) != nil {
				t.Fatal("invalid JSON")
			}
			rules := config["route"].(map[string]any)["rules"].([]any)
			if policy == "rule" && len(rules) != 6 || policy == "global" && len(rules) != 4 {
				t.Fatal("unexpected policy rules")
			}
			if core := os.Getenv("RPL_TEST_CORE"); core != "" {
				path := filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
					t.Fatalf("%s/%s: %v %s", node.Type, policy, err, out)
				}
			}
		}
	}
}

func TestStateCorruptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(dir); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestRouterRejectsPublicDemoKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin.key"), []byte("routerlite-demo"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewServer(dir, "router", &recordingDriver{}, DemoDevice()); err == nil {
		t.Fatal("router accepted the public demo key")
	}
}
