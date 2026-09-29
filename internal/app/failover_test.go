package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func failoverHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	v := h.success(t, "/api/subscription", `{"url":"demo://starter"}`)
	config := FailoverConfig{Enabled: true, Nodes: []string{v.Nodes[0].ID, v.Nodes[1].ID, v.Nodes[2].ID}}
	body, _ := json.Marshal(map[string]any{"enabled": true, "failover": config})
	h.success(t, "/api/settings", string(body))
	h.s.mode = "router"
	return h
}
func TestFailoverRequiresThreeFailuresAndPersists(t *testing.T) {
	h := failoverHarness(t)
	current := h.s.state.Selected
	ids := h.s.state.Failover.Nodes
	calls := []int{}
	h.s.Probe = func(_ context.Context, port int) error {
		calls = append(calls, port)
		if port == probePort+3 {
			return nil
		}
		return errors.New("unreachable")
	}
	now := time.Now()
	for i := 0; i < 2; i++ {
		h.s.monitorStep(context.Background(), now.Add(time.Duration(i)*failoverInterval))
	}
	if h.s.state.Selected != current || len(calls) != 2 {
		t.Fatal("switched on transient failures")
	}
	h.s.monitorStep(context.Background(), now.Add(2*failoverInterval))
	if h.s.state.Selected != ids[2] || h.s.health.Status != "healthy" {
		t.Fatal("did not skip failed backup")
	}
	saved, err := LoadState(h.s.dir)
	if err != nil || saved.Selected != ids[2] {
		t.Fatal("automatic selection not persisted")
	}
	if h.driver.state.Selected != ids[2] {
		t.Fatal("runtime was not switched")
	}
	h.s.Probe = func(context.Context, int) error { return nil }
	h.s.monitorStep(context.Background(), now.Add(3*failoverInterval))
	if h.s.state.Selected != ids[2] {
		t.Fatal("healthy node unnecessarily replaced")
	}
}
func TestFailoverRecoveryResetsStreakAndAllDownIsBounded(t *testing.T) {
	h := failoverHarness(t)
	current := h.s.state.Selected
	bad := true
	calls := 0
	h.s.Probe = func(context.Context, int) error {
		calls++
		if bad {
			return errors.New("failed")
		}
		return nil
	}
	now := time.Now()
	h.s.monitorStep(context.Background(), now)
	bad = false
	h.s.monitorStep(context.Background(), now.Add(failoverInterval))
	if h.s.health.Failures != 0 {
		t.Fatal("success did not reset streak")
	}
	bad = true
	for i := 2; i < 5; i++ {
		h.s.monitorStep(context.Background(), now.Add(time.Duration(i)*failoverInterval))
	}
	if h.s.state.Selected != current || h.s.health.Status != "unavailable" {
		t.Fatal("all-down changed selection")
	}
	before := calls
	h.s.monitorStep(context.Background(), now.Add(5*failoverInterval))
	if calls != before+1 {
		t.Fatal("backup sweep ignored cooldown")
	}
	h.s.monitorStep(context.Background(), now.Add(9*failoverInterval))
	if calls != before+4 {
		t.Fatal("backup sweep did not resume after cooldown")
	}
}
func TestFailoverIgnoresLateProbeAfterManualChange(t *testing.T) {
	h := failoverHarness(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.s.Probe = func(ctx context.Context, _ int) error { close(started); <-release; return errors.New("late failure") }
	go func() { defer close(done); h.s.monitorStep(context.Background(), time.Now()) }()
	<-started
	// This must finish while the probe is blocked: health checks cannot hold s.mu.
	body, _ := json.Marshal(map[string]string{"selected": h.s.state.Subscription.Nodes[3].ID})
	result := h.success(t, "/api/settings", string(body))
	close(release)
	<-done
	if h.s.state.Selected != result.Selected || h.s.state.Failover.Enabled || h.s.health.Failures != 0 {
		t.Fatal("stale result overrode manual change")
	}
}
func TestFailoverSaveFailureRestoresSelection(t *testing.T) {
	h := failoverHarness(t)
	original := h.s.state.Selected
	h.s.Probe = func(_ context.Context, port int) error {
		if port == probePort {
			return errors.New("failed")
		}
		return nil
	}
	h.s.Save = func(string, State) error { return errors.New("disk full") }
	for i := 0; i < 3; i++ {
		h.s.monitorStep(context.Background(), time.Now().Add(time.Duration(i)*failoverInterval))
	}
	if h.s.state.Selected != original || h.driver.state.Selected != original || h.s.health.Status != "unavailable" {
		t.Fatal("failed save did not preserve original")
	}
}
func TestFailoverPausedModesNeverProbe(t *testing.T) {
	for _, mode := range []string{"disabled", "direct", "stopped", "demo", "off"} {
		t.Run(mode, func(t *testing.T) {
			h := failoverHarness(t)
			switch mode {
			case "disabled":
				h.s.state.Enabled = false
			case "direct":
				h.s.state.Policy = "direct"
			case "stopped":
				h.driver.state.Enabled = false
			case "demo":
				h.s.mode = "demo"
			case "off":
				h.s.state.Failover.Enabled = false
			}
			h.s.Probe = func(context.Context, int) error { t.Fatal("unexpected probe"); return nil }
			h.s.monitorStep(context.Background(), time.Now())
		})
	}
}
func TestFailoverValidationAndSubscriptionPruning(t *testing.T) {
	h := failoverHarness(t)
	nodes := h.s.state.Subscription.Nodes
	for _, config := range []FailoverConfig{
		{true, []string{nodes[0].ID}},
		{true, []string{nodes[0].ID, nodes[0].ID}},
		{true, []string{nodes[1].ID, nodes[2].ID}},
		{false, []string{"missing"}},
		{false, []string{"1", "2", "3", "4", "5", "6"}},
	} {
		body, _ := json.Marshal(map[string]any{"failover": config})
		if h.request("POST", "/api/settings", string(body), true).Code == 200 {
			t.Fatal("accepted invalid pool")
		}
	}
	oldPool := append([]string{}, h.s.state.Failover.Nodes...)
	next := h.s.state
	next.Subscription.Nodes = next.Subscription.Nodes[1:]
	next.Enabled = false
	next.pruneFailover()
	if next.Failover.Enabled || len(next.Failover.Nodes) != 2 || len(h.s.state.Failover.Nodes) != len(oldPool) {
		t.Fatal("pruning corrupts state or stays enabled")
	}
}
func TestFailoverConfigForcesExactOutboundAndRealCore(t *testing.T) {
	h := failoverHarness(t)
	s := h.s.state
	assets := os.Getenv("RPL_TEST_ASSETS")
	if assets == "" {
		assets = t.TempDir()
	}
	for _, policy := range []string{"rule", "global"} {
		s.Policy = policy
		data, err := RenderConfig(s, DemoDevice(), assets)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err = json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		rules := config["route"].(map[string]any)["rules"].([]any)
		ins := config["inbounds"].([]any)
		if len(ins) != 7 || len(config["outbounds"].([]any)) != 4 {
			t.Fatal("unexpected core count")
		}
		for i := 0; i < 4; i++ {
			rule := rules[i].(map[string]any)
			expected := "proxy"
			if i == 2 {
				expected = "backup-1"
			}
			if i == 3 {
				expected = "backup-2"
			}
			if rule["outbound"] != expected {
				t.Fatal("probe could escape through direct/another node")
			}
			inbound := ins[3+i].(map[string]any)
			if inbound["listen"] != "127.0.0.1" || int(inbound["listen_port"].(float64)) != probePort+i {
				t.Fatal("probe endpoint exposed or unstable")
			}
		}
		for _, node := range s.Subscription.Nodes {
			if node.Outbound["tag"] != "proxy" {
				t.Fatal("render mutated stored node")
			}
		}
		if core := os.Getenv("RPL_TEST_CORE"); core != "" {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
		}
	}
}
func TestMonitoringCloseIsIdempotent(t *testing.T) {
	h := failoverHarness(t)
	h.s.StartMonitoring()
	h.s.StartMonitoring()
	var group sync.WaitGroup
	for i := 0; i < 3; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := h.s.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	h.s.StartMonitoring()
}

func TestHTTPSProbeUsesOnlyLocalProxyAndHonorsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:17900")
	if err != nil {
		t.Skip("fixed probe port already occupied")
	}
	var mu sync.Mutex
	hosts := []string{}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodConnect {
			t.Error("probe did not use HTTPS CONNECT")
		}
		hosts = append(hosts, r.Host)
		w.WriteHeader(http.StatusBadGateway)
	}))
	proxy.Listener = listener
	proxy.Start()
	defer proxy.Close()
	if probeHTTPS(context.Background(), probePort) == nil {
		t.Fatal("failed proxy accepted")
	}
	mu.Lock()
	if len(hosts) != 2 || hosts[0] != "www.gstatic.com:443" || hosts[1] != "www.cloudflare.com:443" {
		t.Errorf("wrong probe destinations: %v", hosts)
	}
	mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(probeHTTPS(ctx, probePort), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if probeHTTPS(context.Background(), 80) == nil {
		t.Fatal("unexpected proxy port allowed")
	}
}
