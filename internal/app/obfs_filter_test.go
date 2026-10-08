package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const obfsFixture = `proxies:
 - {name: Singapore 01, type: ss, server: edge.example.invalid, port: 12001, cipher: aes-128-gcm, password: fixture-only, plugin: obfs, plugin-opts: {mode: http, host: www.example.invalid}}
 - {name: Singapore 02, type: ss, server: edge.example.invalid, port: 12002, cipher: aes-128-gcm, password: fixture-only, plugin: obfs, plugin-opts: {mode: tls, host: www.example.invalid}}
`

func TestObfsConversionAndIdentity(t *testing.T) {
	nodes, warnings, err := ParseSubscription([]byte(obfsFixture))
	if err != nil || len(nodes) != 2 || len(warnings) != 0 {
		t.Fatalf("unexpected parse: %v", err)
	}
	if nodes[0].Outbound["plugin"] != "obfs-local" || nodes[0].Outbound["plugin_opts"] != "obfs=http;obfs-host=www.example.invalid" || nodes[1].Outbound["plugin_opts"] != "obfs=tls;obfs-host=www.example.invalid" {
		t.Fatal("plugin conversion lost transport fields")
	}
	renamed, _, err := ParseSubscription([]byte(strings.ReplaceAll(obfsFixture, "Singapore 02", "Renamed")))
	if err != nil || renamed[1].ID != nodes[1].ID {
		t.Fatal("rename changed identity")
	}
	changed, _, _ := ParseSubscription([]byte(strings.ReplaceAll(obfsFixture, "mode: tls", "mode: http")))
	if changed[1].ID == nodes[1].ID {
		t.Fatal("different obfs modes share identity")
	}
	for _, bad := range []string{
		strings.ReplaceAll(obfsFixture, "plugin: obfs", "plugin: v2ray-plugin"),
		strings.ReplaceAll(obfsFixture, "mode: http", "mode: invalid"),
		strings.ReplaceAll(obfsFixture, "host: www.example.invalid", "host: 'host;obfs=tls'"),
		strings.ReplaceAll(obfsFixture, "host: www.example.invalid", "host: 'host\\r\\nheader'"),
		strings.ReplaceAll(obfsFixture, "host: www.example.invalid", "host: www.example.invalid, unsupported: true"),
	} {
		if nodes, _, _ := ParseSubscription([]byte(bad)); len(nodes) != 0 && !strings.Contains(bad, "mode: invalid") {
			t.Fatal("unsafe or unsupported plugin accepted")
		}
	}
}

func TestFilteredImportRefreshAndFailedFilterPreserveState(t *testing.T) {
	h := newHarness(t)
	h.s.Fetch = func(context.Context, string) ([]byte, error) { return []byte(obfsFixture), nil }
	v := h.success(t, "/api/subscription", `{"url":"https://provider.example/sub","name":"Filtered","nodeFilter":"Singapore 02"}`)
	if len(v.Nodes) != 1 || v.Nodes[0].Name != "Singapore 02" || v.Subscriptions[0].NodeFilter != "Singapore 02" {
		t.Fatal("filter not saved")
	}
	h.success(t, "/api/settings", `{"enabled":true}`)
	h.success(t, "/api/subscription/refresh", `{}`)
	saved, err := LoadState(h.s.dir)
	if err != nil || saved.Subscription.NodeFilter != "Singapore 02" || len(saved.Subscription.Nodes) != 1 {
		t.Fatal("refresh or reload lost filter", err)
	}
	before, _ := json.Marshal(h.s.state)
	if r := h.request("POST", "/api/subscription", `{"url":"https://provider.example/sub","nodeFilter":"missing"}`, true); r.Code != 400 {
		t.Fatal("missing filter accepted")
	}
	after, _ := json.Marshal(h.s.state)
	if string(before) != string(after) || !h.s.state.Enabled {
		t.Fatal("failed filter changed active state")
	}
}

func TestFilterBeforeNodeCountLimit(t *testing.T) {
	data := strings.Replace(obfsFixture, "proxies:\n", "proxies:\n"+strings.Repeat(" - {name: other, type: ss, server: other.invalid, port: 443, cipher: aes-128-gcm, password: dummy}\n", MaxNodes), 1)
	nodes, _, err := parseSubscription([]byte(data), "Singapore 02")
	if err != nil || len(nodes) != 1 {
		t.Fatal("filter did not reduce imported nodes", err)
	}
	if _, _, err = ParseSubscription([]byte(data)); err == nil {
		t.Fatal("unfiltered limit ignored")
	}
}

func TestOneTimeSubscriptionDoesNotPersistURL(t *testing.T) {
	h := newHarness(t)
	calls := 0
	h.s.Fetch = func(_ context.Context, raw string) ([]byte, error) {
		if raw != "https://provider.example/sub?token=ephemeral-secret" {
			t.Fatal("wrong fetch URL")
		}
		calls++
		return []byte(obfsFixture), nil
	}
	v := h.success(t, "/api/subscription", `{"url":"https://provider.example/sub?token=ephemeral-secret","oneTime":true,"nodeFilter":"Singapore 02"}`)
	if calls != 1 || len(v.Nodes) != 1 || v.Subscriptions[0].CanRefresh {
		t.Fatal("one-time import failed")
	}
	saved, err := LoadState(h.s.dir)
	if err != nil || saved.Subscription.URL != "" || len(saved.Subscription.Nodes) != 1 || saved.Subscription.NodeFilter != "Singapore 02" {
		t.Fatal("one-time state invalid", err)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "ephemeral-secret") || strings.Contains(string(raw), "provider.example") {
		t.Fatal("temporary URL retained")
	}
	if r := h.request("POST", "/api/subscription/refresh", `{}`, true); r.Code != 400 || calls != 1 {
		t.Fatal("expired URL was reused")
	}
}
