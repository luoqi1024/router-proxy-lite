package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionsKeepActiveAndRestoreSelections(t *testing.T) {
	h := newHarness(t)
	h.s.Fetch = func(context.Context, string) ([]byte, error) { return DemoSubscription(), nil }
	first := h.success(t, "/api/subscription", `{"url":"https://one.example/sub?token=secret-one","name":"One"}`)
	selected := first.Nodes[1].ID
	h.success(t, "/api/settings", `{"enabled":true,"selected":"`+selected+`","failover":{"enabled":true,"nodes":["`+selected+`","`+first.Nodes[0].ID+`"]}}`)
	calls, revision := h.driver.calls, h.s.revision
	second := h.success(t, "/api/subscription", `{"url":"https://two.example/sub?token=secret-two","name":"Two"}`)
	if second.Selected != selected || !second.Running || second.ActiveSubscription != first.ActiveSubscription || len(second.Subscriptions) != 2 || h.driver.calls != calls || h.s.revision != revision {
		t.Fatal("inactive import changed active proxy")
	}
	var secondID string
	for _, p := range second.Subscriptions {
		if p.Name == "Two" {
			secondID = p.ID
		}
	}
	second = h.success(t, "/api/subscription/switch", `{"id":"`+secondID+`"}`)
	if second.SubscriptionName != "Two" || second.Failover.Enabled || !second.Running {
		t.Fatal("switch did not load second profile")
	}
	h.success(t, "/api/settings", `{"selected":"`+second.Nodes[2].ID+`"}`)
	first = h.success(t, "/api/subscription/switch", `{"id":"`+first.ActiveSubscription+`"}`)
	if first.Selected != selected || !first.Failover.Enabled || len(first.Failover.Nodes) != 2 {
		t.Fatal("first selection/failover lost")
	}
	second = h.success(t, "/api/subscription/switch", `{"id":"`+secondID+`"}`)
	if second.Selected != second.Nodes[2].ID {
		t.Fatal("second selection lost")
	}
	persisted, err := LoadState(h.s.dir)
	if err != nil || persisted.Subscription.ID != secondID || len(persisted.SavedSubscriptions) != 1 {
		t.Fatal("profiles did not persist", err)
	}
	response := h.request("GET", "/api/state", "", true).Body.String()
	for _, secret := range []string{"secret-one", "secret-two", "demo-only", "https://", "outbound"} {
		if strings.Contains(response, secret) {
			t.Fatal("profile credentials exposed")
		}
	}
}

func TestSubscriptionsLimitsUpdateAndDelete(t *testing.T) {
	h := newHarness(t)
	h.s.Fetch = func(context.Context, string) ([]byte, error) { return DemoSubscription(), nil }
	one := h.success(t, "/api/subscription", `{"url":"https://one.example/sub"}`)
	h.success(t, "/api/subscription", `{"url":"https://two.example/sub"}`)
	v := h.success(t, "/api/subscription", `{"url":"https://three.example/sub"}`)
	if len(v.Subscriptions) != 3 {
		t.Fatal("missing saved subscriptions")
	}
	before, _ := json.Marshal(h.s.state)
	if w := h.request("POST", "/api/subscription", `{"url":"https://four.example/sub"}`, true); w.Code != 409 {
		t.Fatal("fourth profile accepted")
	}
	after, _ := json.Marshal(h.s.state)
	if string(before) != string(after) {
		t.Fatal("limit error changed profiles")
	}
	calls := h.driver.calls
	h.success(t, "/api/subscription", `{"url":"https://two.example/sub","name":"Renamed"}`)
	if h.driver.calls != calls || h.s.state.subscriptionCount() != 3 {
		t.Fatal("same URL consumed slot/restarted proxy")
	}
	h.success(t, "/api/settings", `{"enabled":true}`)
	if h.request("POST", "/api/subscription/delete", `{"id":"`+one.ActiveSubscription+`"}`, true).Code != 409 {
		t.Fatal("deleted enabled active subscription")
	}
	h.success(t, "/api/subscription/delete", `{"id":"`+v.Subscriptions[2].ID+`"}`)
	h.success(t, "/api/settings", `{"enabled":false}`)
	v = h.success(t, "/api/subscription/delete", `{"id":"`+one.ActiveSubscription+`"}`)
	if len(v.Subscriptions) != 1 || v.SubscriptionName != "Renamed" {
		t.Fatal("active delete did not select remaining profile")
	}
	v = h.success(t, "/api/subscription/delete", `{"id":"`+v.ActiveSubscription+`"}`)
	if len(v.Subscriptions) != 0 || len(v.Nodes) != 0 || v.Selected != "" {
		t.Fatal("last delete retained nodes")
	}
}

func TestSubscriptionFailuresPreserveProfilesAndRuntime(t *testing.T) {
	h := newHarness(t)
	h.success(t, "/api/subscription", `{"url":"demo://starter"}`)
	h.success(t, "/api/settings", `{"enabled":true}`)
	v := h.success(t, "/api/subscription", `{"content":"proxies: [{name: other, type: anytls, server: other.invalid, port: 443, password: test-only}]"}`)
	id := v.Subscriptions[1].ID
	before, _ := json.Marshal(h.s.state)
	h.driver.fail = true
	if h.request("POST", "/api/subscription/switch", `{"id":"`+id+`"}`, true).Code != 409 {
		t.Fatal("driver failure ignored")
	}
	h.driver.fail = false
	h.s.Save = func(string, State) error { return errors.New("disk full") }
	if h.request("POST", "/api/subscription/switch", `{"id":"`+id+`"}`, true).Code != 409 || !h.driver.Running() {
		t.Fatal("save failure lost runtime")
	}
	if h.request("POST", "/api/subscription/delete", `{"id":"`+id+`"}`, true).Code != 409 {
		t.Fatal("delete save failure ignored")
	}
	if h.request("POST", "/api/subscription", `{"url":"demo://starter","name":"Changed"}`, true).Code != 409 {
		t.Fatal("active update save failure ignored")
	}
	if h.request("POST", "/api/subscription", `{"content":"proxies: [{name: extra, type: anytls, server: extra.invalid, port: 443, password: test-only}]"}`, true).Code != 409 {
		t.Fatal("inactive save failure ignored")
	}
	after, _ := json.Marshal(h.s.state)
	if string(before) != string(after) {
		t.Fatal("failed mutation changed stored profiles")
	}
}

func TestSubscriptionStorageCapAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	nodes, _, _ := ParseSubscription(DemoSubscription())
	legacy := DefaultState()
	legacy.Schema = 1
	legacy.Subscription = Subscription{Name: "Legacy", Nodes: nodes}
	legacy.Selected = nodes[0].ID
	b, _ := json.Marshal(legacy)
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(dir)
	if err != nil || s.Schema != 2 || s.Subscription.ID == "" || s.Selected != legacy.Selected {
		t.Fatal("legacy migration lost state", err)
	}
	if err := SaveState(dir, s); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	s.Subscription.Nodes[0].Outbound["password"] = strings.Repeat("x", MaxStoredStateBytes)
	if err := SaveState(dir, s); err == nil {
		t.Fatal("oversized state accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("oversized state replaced valid disk state")
	}
	var disk map[string]any
	if json.Unmarshal(before, &disk) != nil || disk["schema"] != float64(2) {
		t.Fatal("migration not persisted")
	}
}
