package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const MaxFailoverNodes = 5
const probePort = 17900
const failoverInterval = 30 * time.Second
const failoverCooldown = 2 * time.Minute
const failoverFailures = 3

type FailoverConfig struct {
	Enabled bool     `json:"enabled"`
	Nodes   []string `json:"nodes"`
}
type HealthStatus struct {
	Status       string    `json:"status"`
	Failures     int       `json:"failures"`
	CheckedAt    time.Time `json:"checkedAt"`
	LastSwitchAt time.Time `json:"lastSwitchAt"`
}

func containsNode(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
func (s State) validateFailover() error {
	if len(s.Failover.Nodes) > MaxFailoverNodes {
		return errors.New("自动切换名单最多 5 个节点")
	}
	seen := map[string]bool{}
	for _, id := range s.Failover.Nodes {
		if seen[id] {
			return errors.New("自动切换名单不能重复")
		}
		seen[id] = true
		found := false
		for _, n := range s.Subscription.Nodes {
			if n.ID == id {
				found = true
				break
			}
		}
		if !found {
			return errors.New("自动切换名单中有已失效的节点")
		}
	}
	if s.Failover.Enabled && (len(seen) < 2 || !seen[s.Selected]) {
		return errors.New("请勾选当前节点和至少一个备用节点，再开启自动切换")
	}
	return nil
}
func (s *State) pruneFailover() {
	kept := []string{}
	for _, id := range s.Failover.Nodes {
		for _, n := range s.Subscription.Nodes {
			if n.ID == id {
				kept = append(kept, id)
				break
			}
		}
	}
	changed := len(kept) != len(s.Failover.Nodes)
	s.Failover.Nodes = kept
	if s.Failover.Enabled && (len(kept) < 2 || !containsNode(kept, s.Selected)) {
		s.Failover.Enabled = false
		s.Subscription.Warnings = append(s.Subscription.Warnings, "自动切换已暂停：请重新确认备用节点名单")
	} else if changed {
		s.Subscription.Warnings = append(s.Subscription.Warnings, "已移除订阅中失效的备用节点")
	}
}

// Every check is forced through a dedicated loopback inbound and its exact
// outbound. Smart-routing direct rules cannot turn a failed proxy into a pass.
// No second core process is created and certificates are always verified.
func probeHTTPS(ctx context.Context, port int) error {
	if port < probePort || port > probePort+MaxFailoverNodes {
		return errors.New("invalid probe port")
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, target := range []string{"https://www.gstatic.com/generate_204", "https://www.cloudflare.com/cdn-cgi/trace"} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 && readErr == nil {
			return nil
		}
	}
	return errors.New("节点 HTTPS 探测失败")
}
func (s *Server) healthView() HealthStatus {
	h := s.health
	switch {
	case !s.state.Failover.Enabled:
		h.Status = "disabled"
	case s.mode == "demo":
		h.Status = "demo"
	case !s.state.Enabled || s.state.Policy == "direct" || !s.driver.Running():
		h.Status = "paused"
	case h.Status == "":
		h.Status = "waiting"
	}
	return h
}
func (s *Server) monitorEligible() bool {
	return s.mode == "router" && s.state.Enabled && s.state.Policy != "direct" && s.state.Failover.Enabled && s.driver.Running()
}
func (s *Server) StartMonitoring() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.monitorDone != nil || s.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.monitorCancel = cancel
	s.monitorDone = make(chan struct{})
	go func() {
		defer close(s.monitorDone)
		ticker := time.NewTicker(failoverInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.monitorStep(ctx, time.Now())
			}
		}
	}()
}

// Network requests run without the server lock. A manual change cancels the
// cycle and advances revision; stale probe results must never overwrite it.
func (s *Server) monitorStep(parent context.Context, now time.Time) {
	started := time.Now()
	completedAt := func() time.Time { return now.Add(time.Since(started)) }
	s.mu.Lock()
	if !s.monitorEligible() {
		s.mu.Unlock()
		return
	}
	snapshot, revision := s.state, s.revision
	ctx, cancel := context.WithCancel(parent)
	s.probeCancel = cancel
	s.health.Status = "checking"
	s.mu.Unlock()
	defer cancel()
	err := s.Probe(ctx, probePort)
	s.mu.Lock()
	if ctx.Err() != nil || s.revision != revision || !s.monitorEligible() {
		s.mu.Unlock()
		return
	}
	s.health.CheckedAt = completedAt()
	if err == nil {
		s.health.Failures = 0
		s.health.Status = "healthy"
		s.mu.Unlock()
		return
	}
	s.health.Failures++
	s.health.Status = "retrying"
	if s.health.Failures < failoverFailures {
		s.mu.Unlock()
		return
	}
	if completedAt().Before(s.nextFallback) {
		s.health.Status = "unavailable"
		s.mu.Unlock()
		return
	}
	s.health.Status = "switching"
	s.nextFallback = now.Add(failoverCooldown)
	s.mu.Unlock()
	for i, id := range snapshot.Failover.Nodes {
		if id == snapshot.Selected {
			continue
		}
		err = s.Probe(ctx, probePort+1+i)
		s.mu.Lock()
		if ctx.Err() != nil || s.revision != revision || !s.monitorEligible() {
			s.mu.Unlock()
			return
		}
		if err != nil {
			s.mu.Unlock()
			continue
		}
		next := s.state
		next.Selected = id
		// commit cancels this probe cycle. Use a separate bounded apply context.
		applyCtx, applyCancel := context.WithTimeout(parent, 60*time.Second)
		applyErr := s.commit(applyCtx, next)
		applyCancel()
		completed := completedAt()
		s.nextFallback = completed.Add(failoverCooldown)
		if applyErr == nil {
			s.health = HealthStatus{Status: "healthy", CheckedAt: completed, LastSwitchAt: completed}
			s.event("当前节点连续连接失败，已自动切换到可用备用节点")
		} else {
			s.health.Status = "unavailable"
			s.event("备用节点切换未完成，已尝试保留原有配置")
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if s.revision == revision && ctx.Err() == nil {
		s.health.Status = "unavailable"
		s.nextFallback = completedAt().Add(failoverCooldown)
		s.event("备用节点暂时均不可用，保留当前选择并稍后重试")
	}
	s.mu.Unlock()
}
