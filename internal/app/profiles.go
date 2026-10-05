package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const MaxSubscriptions = 3
const MaxStoredStateBytes = 512 << 10

// The active subscription stays in the original fields. Only inactive profiles
// are stored here, so node credentials are never duplicated on flash.
type SubscriptionProfile struct {
	Subscription Subscription   `json:"subscription"`
	Selected     string         `json:"selected"`
	Failover     FailoverConfig `json:"failover"`
}

type PublicSubscription struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NodeCount  int    `json:"nodeCount"`
	CanRefresh bool   `json:"canRefresh"`
}

func (s *State) normalizeSubscriptions() {
	s.Schema = 2
	if len(s.Subscription.Nodes) != 0 && s.Subscription.ID == "" {
		s.Subscription.ID = "legacy"
	}
}

func (s State) activeProfile() SubscriptionProfile {
	return SubscriptionProfile{s.Subscription, s.Selected, s.Failover}
}

func (s *State) activateProfile(p SubscriptionProfile) {
	s.Subscription, s.Selected, s.Failover = p.Subscription, p.Selected, p.Failover
}

func (s State) subscriptionCount() int {
	n := len(s.SavedSubscriptions)
	if len(s.Subscription.Nodes) != 0 {
		n++
	}
	return n
}

func (s State) validateSubscriptions() error {
	if len(s.Subscription.Nodes) > MaxNodes {
		return errors.New("节点超过 512 个")
	}
	if s.subscriptionCount() > MaxSubscriptions {
		return errors.New("最多保存 3 套订阅，请先删除不需要的订阅")
	}
	if len(s.SavedSubscriptions) != 0 && len(s.Subscription.Nodes) == 0 {
		return errors.New("订阅配置缺少当前订阅")
	}
	seen := map[string]bool{}
	if len(s.Subscription.Nodes) != 0 {
		seen[s.Subscription.ID] = true
	}
	for _, p := range s.SavedSubscriptions {
		if p.Subscription.ID == "" || seen[p.Subscription.ID] || len(p.Subscription.Nodes) == 0 || len(p.Subscription.Nodes) > MaxNodes {
			return errors.New("保存的订阅配置无效")
		}
		seen[p.Subscription.ID] = true
		check := State{Policy: "rule", Subscription: p.Subscription, Selected: p.Selected, Failover: p.Failover}
		if _, err := check.Node(); err != nil {
			return err
		}
		if err := check.validateFailover(); err != nil {
			return err
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return errors.New("订阅配置无法保存")
	}
	if len(b) > MaxStoredStateBytes {
		return errors.New("已保存配置超过 512 KiB，请精简节点或删除不需要的订阅")
	}
	return nil
}

func newSubscriptionID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("无法创建订阅")
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) publicSubscriptions() []PublicSubscription {
	result := []PublicSubscription{}
	add := func(p Subscription) {
		result = append(result, PublicSubscription{p.ID, p.Name, len(p.Nodes), p.URL != ""})
	}
	if len(s.state.Subscription.Nodes) != 0 {
		add(s.state.Subscription)
	}
	for _, p := range s.state.SavedSubscriptions {
		add(p.Subscription)
	}
	return result
}

// Saving an inactive subscription must not restart the active proxy or cancel
// its health check. Apply only when the active configuration actually changes.
func (s *Server) saveSubscriptions(next State) error {
	next.normalizeSubscriptions()
	if err := next.Validate(); err != nil {
		return err
	}
	if err := s.Save(s.dir, next); err != nil {
		return errors.New("保存失败，原有订阅未改变；请检查存储空间")
	}
	s.state = next
	return nil
}

func (s *Server) switchSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !readBody(w, r, &body) {
		return
	}
	if body.ID == s.state.Subscription.ID && body.ID != "" {
		writeJSON(w, 200, s.view())
		return
	}
	for i, profile := range s.state.SavedSubscriptions {
		if profile.Subscription.ID != body.ID {
			continue
		}
		next := s.state
		next.SavedSubscriptions = append([]SubscriptionProfile(nil), s.state.SavedSubscriptions...)
		next.SavedSubscriptions[i] = s.state.activeProfile()
		next.activateProfile(profile)
		if err := s.commit(r.Context(), next); err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.event("已切换订阅")
		writeJSON(w, 200, s.view())
		return
	}
	fail(w, 404, "订阅不存在")
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !readBody(w, r, &body) {
		return
	}
	next := s.state
	if body.ID == s.state.Subscription.ID && body.ID != "" {
		if s.state.Enabled {
			fail(w, 409, "请先关闭代理，再删除当前订阅")
			return
		}
		if len(next.SavedSubscriptions) != 0 {
			next.activateProfile(next.SavedSubscriptions[0])
			next.SavedSubscriptions = append([]SubscriptionProfile(nil), next.SavedSubscriptions[1:]...)
		} else {
			next.activateProfile(SubscriptionProfile{Subscription: Subscription{Nodes: []Node{}}})
		}
	} else {
		found := false
		next.SavedSubscriptions = []SubscriptionProfile{}
		for _, p := range s.state.SavedSubscriptions {
			if p.Subscription.ID == body.ID {
				found = true
				continue
			}
			next.SavedSubscriptions = append(next.SavedSubscriptions, p)
		}
		if !found {
			fail(w, 404, "订阅不存在")
			return
		}
	}
	if err := s.saveSubscriptions(next); err != nil {
		fail(w, 409, err.Error())
		return
	}
	s.event("订阅已删除")
	writeJSON(w, 200, s.view())
}

func (s *Server) saveImportedProfile(ctx context.Context, nodes []Node, warnings []string, rawURL, name string) error {
	next := s.state
	next.SavedSubscriptions = append([]SubscriptionProfile(nil), s.state.SavedSubscriptions...)
	// Re-importing the same URL updates that profile without consuming a slot.
	if rawURL != "" && rawURL == next.Subscription.URL && len(next.Subscription.Nodes) != 0 {
		return s.replaceActiveSubscription(ctx, nodes, warnings, rawURL, name)
	}
	for i, p := range next.SavedSubscriptions {
		if rawURL == "" || rawURL != p.Subscription.URL {
			continue
		}
		p.Subscription = Subscription{ID: p.Subscription.ID, URL: rawURL, Name: name, UpdatedAt: time.Now().UTC(), Nodes: nodes, Warnings: warnings}
		check := State{Subscription: p.Subscription, Selected: p.Selected, Failover: p.Failover}
		if _, err := check.Node(); err != nil {
			check.Selected = nodes[0].ID
		}
		check.pruneFailover()
		p.Subscription, p.Selected, p.Failover = check.Subscription, check.Selected, check.Failover
		next.SavedSubscriptions[i] = p
		return s.saveSubscriptions(next)
	}
	if next.subscriptionCount() >= MaxSubscriptions {
		return errors.New("最多保存 3 套订阅，请先删除不需要的订阅")
	}
	id, err := newSubscriptionID()
	if err != nil {
		return err
	}
	p := SubscriptionProfile{Subscription: Subscription{ID: id, URL: rawURL, Name: name, UpdatedAt: time.Now().UTC(), Nodes: nodes, Warnings: warnings}, Selected: nodes[0].ID}
	if len(next.Subscription.Nodes) == 0 {
		next.activateProfile(p)
		next.Enabled = false
		return s.commit(ctx, next)
	}
	next.SavedSubscriptions = append(next.SavedSubscriptions, p)
	return s.saveSubscriptions(next)
}
