package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	mu            sync.Mutex
	state         State
	dir           string
	mode          string
	key           [32]byte
	setupRequired bool
	driver        Driver
	device        Device
	events        []Event
	sessions      map[string]time.Time
	failures      int
	blockedUntil  time.Time
	Fetch         func(context.Context, string) ([]byte, error)
	Save          func(string, State) error
	WriteKey      func(string, []byte) error
	Probe         func(context.Context, int) error
	revision      uint64
	health        HealthStatus
	nextFallback  time.Time
	probeCancel   context.CancelFunc
	monitorCancel context.CancelFunc
	monitorDone   chan struct{}
	closeOnce     sync.Once
	closeErr      error
	closed        bool
}

func NewServer(dir, mode string, driver Driver, device Device) (*Server, string, error) {
	if mode != "demo" && mode != "router" {
		return nil, "", errors.New("invalid mode")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}
	state, err := LoadState(dir)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "admin.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if _, stateErr := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(stateErr) {
			return nil, "", errors.New("已有配置的管理凭据缺失，请从备份恢复；不会重新开放首次设置")
		}
		key = []byte(setupMarker)
		if err = AtomicWrite(path, key); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	setupRequired := string(key) == setupMarker
	if !setupRequired && utf8.RuneCountInString(string(key)) < MinPasswordLength {
		return nil, "", errors.New("admin key is too short")
	}
	if mode == "router" && string(key) == "routerlite-demo" {
		return nil, "", errors.New("此目录使用公开演示口令；请为路由器选择独立的数据目录")
	}
	s := &Server{state: state, dir: dir, mode: mode, key: sha256.Sum256(key), driver: driver, device: device, sessions: map[string]time.Time{}, Fetch: FetchSubscription, Save: SaveState}
	s.setupRequired = setupRequired
	s.Probe = probeHTTPS
	s.WriteKey = AtomicWrite
	s.event("管理服务已启动")
	return s, "", nil
}
func (s *Server) event(message string) {
	s.events = append(s.events, Event{Time: time.Now(), Message: message})
	if len(s.events) > 30 {
		s.events = s.events[len(s.events)-30:]
	}
}
func (s *Server) view() View {
	nodes := []PublicNode{}
	for _, n := range s.state.Subscription.Nodes {
		nodes = append(nodes, PublicNode{n.ID, n.Name, n.Type})
	}
	return View{Version: Version, Mode: s.mode, Enabled: s.state.Enabled, Running: s.driver.Running(), Policy: s.state.Policy, Selected: s.state.Selected, Nodes: nodes, SubscriptionName: s.state.Subscription.Name, UpdatedAt: s.state.Subscription.UpdatedAt, Warnings: s.state.Subscription.Warnings, Events: s.events, Device: s.device, Failover: s.state.Failover, Health: s.healthView(), Subscriptions: s.publicSubscriptions(), ActiveSubscription: s.state.Subscription.ID, MaxSubscriptions: MaxSubscriptions}
}
func (s *Server) commit(ctx context.Context, next State) error {
	next.normalizeSubscriptions()
	if err := next.Validate(); err != nil {
		return err
	}
	old := s.state
	if err := s.driver.Apply(ctx, next); err != nil {
		s.event("配置应用失败，已尝试恢复原有配置")
		return err
	}
	if err := s.Save(s.dir, next); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := s.driver.Apply(restoreCtx, old); e != nil {
			return errors.New("保存失败，运行状态也未能恢复，请停止代理后检查存储")
		}
		return errors.New("保存失败，原有配置已恢复")
	}
	s.state = next
	s.revision++
	if s.probeCancel != nil {
		s.probeCancel()
	}
	s.health = HealthStatus{}
	s.nextFallback = time.Time{}
	return nil
}
func (s *Server) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupRequired {
		return nil
	}
	if s.mode == "demo" {
		return s.driver.Apply(ctx, s.state)
	}
	if err := s.driver.Apply(ctx, s.state); err != nil {
		s.event("开机恢复失败，请检查网络规则与设备条件")
		return err
	}
	return nil
}
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.monitorCancel != nil {
			s.monitorCancel()
		}
		done := s.monitorDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		s.closeErr = s.driver.Close()
	})
	return s.closeErr
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "需要 JSON 请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxSubscriptionBytes+4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "请求格式无效或过大")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, "请求包含多余内容")
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	assets, _ := fs.Sub(webFiles, "web")
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && r.Method != "HEAD" {
				fail(w, 405, "不支持的方法")
				return
			}
			files.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				fail(w, 403, "跨站请求被拒绝")
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "跨站请求被拒绝")
			return
		}
		if r.URL.Path == "/api/setup" {
			s.mu.Lock()
			defer s.mu.Unlock()
			switch r.Method {
			case "GET":
				writeJSON(w, 200, map[string]bool{"required": s.setupRequired})
			case "POST":
				if !s.setupRequired {
					fail(w, 409, "已经设置过管理密码，请登录")
					return
				}
				s.setPassword(w, r, true)
			default:
				fail(w, 405, "不支持的方法")
			}
			return
		}
		if r.URL.Path == "/api/session" && r.Method == "POST" {
			s.login(w, r)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		cookie, err := r.Cookie("rpl_session")
		if err != nil || !s.sessions[cookie.Value].After(time.Now()) {
			fail(w, 401, "请先登录")
			return
		}
		switch {
		case r.URL.Path == "/api/session" && r.Method == "DELETE":
			delete(s.sessions, cookie.Value)
			http.SetCookie(w, &http.Cookie{Name: "rpl_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			writeJSON(w, 200, map[string]bool{"ok": true})
		case r.URL.Path == "/api/state" && r.Method == "GET":
			writeJSON(w, 200, s.view())
		case r.URL.Path == "/api/subscription" && r.Method == "POST":
			s.importSubscription(w, r)
		case r.URL.Path == "/api/subscription/refresh" && r.Method == "POST":
			s.refresh(w, r)
		case r.URL.Path == "/api/subscription/switch" && r.Method == "POST":
			s.switchSubscription(w, r)
		case r.URL.Path == "/api/subscription/delete" && r.Method == "POST":
			s.deleteSubscription(w, r)
		case r.URL.Path == "/api/settings" && r.Method == "POST":
			s.settings(w, r)
		case r.URL.Path == "/api/password" && r.Method == "POST":
			s.changePassword(w, r)
		default:
			fail(w, 404, "接口不存在")
		}
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !readBody(w, r, &body) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupRequired {
		fail(w, 409, "请先设置管理密码")
		return
	}
	if time.Now().Before(s.blockedUntil) {
		fail(w, 429, "尝试次数较多，请一分钟后再试")
		return
	}
	sum := sha256.Sum256([]byte(body.Key))
	if subtle.ConstantTimeCompare(sum[:], s.key[:]) != 1 {
		s.failures++
		if s.failures >= 8 {
			s.blockedUntil = time.Now().Add(time.Minute)
			s.failures = 0
		}
		fail(w, 401, "管理口令不正确")
		return
	}
	s.failures = 0
	for id, t := range s.sessions {
		if t.Before(time.Now()) {
			delete(s.sessions, id)
		}
	}
	if len(s.sessions) >= 8 {
		for id := range s.sessions {
			delete(s.sessions, id)
			break
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fail(w, 500, "无法创建会话")
		return
	}
	id := hex.EncodeToString(b)
	s.sessions[id] = time.Now().Add(12 * time.Hour)
	http.SetCookie(w, &http.Cookie{Name: "rpl_session", Value: id, Path: "/", MaxAge: 43200, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, s.view())
}
func (s *Server) importSubscription(w http.ResponseWriter, r *http.Request) {
	var b struct {
		URL        string `json:"url"`
		Name       string `json:"name"`
		Content    string `json:"content"`
		NodeFilter string `json:"nodeFilter"`
		OneTime    bool   `json:"oneTime"`
	}
	if !readBody(w, r, &b) {
		return
	}
	if len(b.Name) > 80 {
		fail(w, 400, "订阅名称过长")
		return
	}
	if b.Name == "" {
		b.Name = "我的订阅"
	}
	b.NodeFilter = strings.TrimSpace(b.NodeFilter)
	if !validNodeFilter(b.NodeFilter) {
		fail(w, 400, "节点筛选需为不超过 256 字节的名称文字")
		return
	}
	if b.URL != "" && b.Content != "" {
		fail(w, 400, "请选择链接或配置内容其中一种")
		return
	}
	var data []byte
	var err error
	if b.URL == "demo://starter" && s.mode == "demo" {
		data = DemoSubscription()
	} else if b.URL != "" {
		data, err = s.Fetch(r.Context(), b.URL)
	} else {
		data = []byte(b.Content)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	sourceURL := b.URL
	if b.OneTime {
		sourceURL = ""
	}
	s.importData(w, r, data, sourceURL, b.Name, b.NodeFilter)
}
func (s *Server) importData(w http.ResponseWriter, r *http.Request, data []byte, rawURL, name, filter string) {
	nodes, warnings, err := parseSubscription(data, filter)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err = s.saveImportedProfile(r.Context(), nodes, warnings, rawURL, name, filter); err != nil {
		fail(w, 409, err.Error())
		return
	}
	s.event("订阅已保存")
	writeJSON(w, 200, s.view())
}
func (s *Server) replaceActiveSubscription(ctx context.Context, nodes []Node, warnings []string, rawURL, name, filter string) error {
	next := s.state
	next.Subscription = Subscription{ID: next.Subscription.ID, URL: rawURL, Name: name, NodeFilter: filter, UpdatedAt: time.Now().UTC(), Nodes: nodes, Warnings: warnings}
	if _, err := next.Node(); err != nil {
		next.Selected = nodes[0].ID
		next.Enabled = false
		warnings = append(warnings, "请确认节点后开启代理")
		next.Subscription.Warnings = warnings
	}
	next.pruneFailover()
	return s.commit(ctx, next)
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readBody(w, r, &body) {
		return
	}
	raw := s.state.Subscription.URL
	if raw == "" {
		fail(w, 400, "此配置没有订阅链接，请重新导入")
		return
	}
	var data []byte
	var err error
	if raw == "demo://starter" && s.mode == "demo" {
		data = DemoSubscription()
	} else {
		data, err = s.Fetch(r.Context(), raw)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.importData(w, r, data, raw, s.state.Subscription.Name, s.state.Subscription.NodeFilter)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled  *bool           `json:"enabled"`
		Policy   *string         `json:"policy"`
		Selected *string         `json:"selected"`
		Failover *FailoverConfig `json:"failover"`
	}
	if !readBody(w, r, &body) {
		return
	}
	next := s.state
	if body.Enabled != nil {
		next.Enabled = *body.Enabled
	}
	if body.Policy != nil {
		next.Policy = *body.Policy
	}
	if body.Selected != nil {
		next.Selected = *body.Selected
		if _, err := next.Node(); err != nil {
			fail(w, 400, "节点不存在")
			return
		}
	}
	if body.Failover != nil {
		next.Failover = *body.Failover
	}
	if body.Selected != nil && body.Failover == nil && !containsNode(next.Failover.Nodes, next.Selected) {
		next.Failover.Enabled = false
	}
	if err := s.commit(r.Context(), next); err != nil {
		fail(w, 409, err.Error())
		return
	}
	if !next.Enabled {
		s.event("代理已关闭，恢复普通上网")
	} else {
		s.event("节点与策略设置已保存")
	}
	writeJSON(w, 200, s.view())
}
