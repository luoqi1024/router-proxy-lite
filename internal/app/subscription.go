package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const MaxSubscriptionBytes = 2 << 20
const MaxNodes = 512

// Requests go only to the provider, never to an external subscription converter.
// Dialing the validated IP rather than the hostname prevents DNS rebinding.
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15"} {
		if netip.MustParsePrefix(p).Contains(ip) {
			return false
		}
	}
	return true
}
func validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("请填写 HTTPS 订阅链接")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAddress(ip) {
		return nil, errors.New("订阅地址不能指向本机或内网")
	}
	return u, nil
}
func FetchSubscription(ctx context.Context, raw string) ([]byte, error) {
	u, err := validateURL(raw)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 10 * time.Second}
	defer tr.CloseIdleConnections()
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("域名解析失败")
		}
		for _, ip := range ips {
			if !publicAddress(ip) {
				return nil, errors.New("订阅地址解析到内网")
			}
		}
		var last error
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		if last == nil {
			last = errors.New("域名没有可用地址")
		}
		return nil, last
	}
	client := &http.Client{Transport: tr, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("重定向次数过多")
		}
		_, err := validateURL(req.URL.String())
		return err
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, errors.New("订阅地址无效")
	}
	req.Header.Set("User-Agent", "Clash.Meta/routerlite-"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("订阅下载失败，请检查链接或网络；原有配置未改变")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("订阅服务返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSubscriptionBytes+1))
	if err != nil {
		return nil, errors.New("订阅读取失败")
	}
	if len(data) > MaxSubscriptionBytes {
		return nil, errors.New("订阅超过 2 MiB 限制")
	}
	return data, nil
}

func ParseSubscription(data []byte) ([]Node, []string, error) {
	if len(data) > MaxSubscriptionBytes {
		return nil, nil, errors.New("订阅过大")
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, errors.New("订阅不是有效的 Clash YAML；请选择机场提供的 Clash / Mihomo 格式")
	}
	if len(doc.Proxies) == 0 {
		return nil, nil, errors.New("没有找到 Clash 节点；暂不支持 URI/Base64 或 sing-box 订阅")
	}
	if len(doc.Proxies) > MaxNodes {
		return nil, nil, errors.New("节点超过 512 个，请使用精简订阅")
	}
	nodes := []Node{}
	skipped := map[string]int{}
	seen := map[string]bool{}
	for _, p := range doc.Proxies {
		n, err := convertNode(p)
		if err != nil {
			skipped[text(p, "type")+": "+err.Error()]++
			continue
		}
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		nodes = append(nodes, n)
	}
	warnings := []string{}
	for reason, count := range skipped {
		warnings = append(warnings, fmt.Sprintf("跳过 %d 个节点（%s）", count, reason))
	}
	sort.Strings(warnings)
	if len(nodes) == 0 {
		return nil, warnings, errors.New("订阅中没有当前支持的节点")
	}
	return nodes, warnings, nil
}
func text(p map[string]any, key string) string  { v, _ := p[key].(string); return v }
func boolean(p map[string]any, key string) bool { v, _ := p[key].(bool); return v }
func number(v any) (int, error)                 { s := fmt.Sprint(v); return strconv.Atoi(s) }
func convertNode(p map[string]any) (Node, error) {
	typ := text(p, "type")
	name := text(p, "name")
	host := text(p, "server")
	port, err := number(p["port"])
	if err != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "\r\n /\\") || name == "" || len(name) > 256 {
		return Node{}, errors.New("节点字段不完整")
	}
	out := map[string]any{"server": host, "server_port": port, "tag": "proxy"}
	switch typ {
	case "anytls":
		out["type"] = "anytls"
		out["password"] = text(p, "password")
	case "ss":
		if text(p, "plugin") != "" {
			return Node{}, errors.New("暂不支持插件")
		}
		out["type"] = "shadowsocks"
		out["method"] = text(p, "cipher")
		out["password"] = text(p, "password")
		if text(p, "cipher") == "" {
			return Node{}, errors.New("缺少加密方式")
		}
	case "trojan":
		out["type"] = "trojan"
		out["password"] = text(p, "password")
	case "vless":
		out["type"] = "vless"
		out["uuid"] = text(p, "uuid")
		if flow := text(p, "flow"); flow != "" {
			out["flow"] = flow
		}
	case "vmess":
		out["type"] = "vmess"
		out["uuid"] = text(p, "uuid")
		out["security"] = text(p, "cipher")
		if out["security"] == "" {
			out["security"] = "auto"
		}
		if v, ok := p["alterId"]; ok {
			n, e := number(v)
			if e != nil || n < 0 {
				return Node{}, errors.New("无效 alterId")
			}
			out["alter_id"] = n
		}
	default:
		return Node{}, errors.New("此协议尚未支持")
	}
	if typ == "vless" || typ == "vmess" {
		if text(p, "uuid") == "" {
			return Node{}, errors.New("缺少 UUID")
		}
	} else if text(p, "password") == "" {
		return Node{}, errors.New("缺少凭证")
	}
	if typ == "anytls" || typ == "trojan" || boolean(p, "tls") {
		tls := map[string]any{"enabled": true, "insecure": boolean(p, "skip-cert-verify")}
		sni := text(p, "sni")
		if sni == "" {
			sni = text(p, "servername")
		}
		if sni != "" {
			tls["server_name"] = sni
		}
		if alpn, ok := p["alpn"]; ok {
			tls["alpn"] = alpn
		}
		if fp := text(p, "client-fingerprint"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if r, ok := p["reality-opts"].(map[string]any); ok {
			if text(r, "public-key") == "" {
				return Node{}, errors.New("缺少 Reality 公钥")
			}
			tls["reality"] = map[string]any{"enabled": true, "public_key": text(r, "public-key"), "short_id": text(r, "short-id")}
		}
		out["tls"] = tls
	}
	network := text(p, "network")
	if (typ == "ss" && boolean(p, "tls")) || ((typ == "ss" || typ == "anytls") && network != "" && network != "tcp") {
		return Node{}, errors.New("此协议不支持所选 TLS/传输组合")
	}
	if network != "" && network != "tcp" {
		if network != "ws" {
			return Node{}, errors.New("暂不支持此传输方式")
		}
		transport := map[string]any{"type": "ws"}
		if opts, ok := p["ws-opts"].(map[string]any); ok {
			if path := text(opts, "path"); path != "" {
				transport["path"] = path
			}
			if headers, ok := opts["headers"]; ok {
				transport["headers"] = headers
			}
		}
		out["transport"] = transport
	}
	return Node{ID: nodeID(out), Name: name, Type: typ, Outbound: out}, nil
}

func DemoSubscription() []byte {
	// Reserved .invalid names, not real credentials or working proxy endpoints.
	return []byte("proxies:\n  - {name: '美国 · 西海岸 12', type: anytls, server: us12.example.invalid, port: 443, password: demo-only}\n  - {name: '日本 · 东京 01', type: trojan, server: jp01.example.invalid, port: 443, password: demo-only}\n  - {name: '新加坡 · 01', type: ss, server: sg01.example.invalid, port: 443, cipher: aes-128-gcm, password: demo-only}\n  - {name: '香港 · 02', type: vmess, server: hk02.example.invalid, port: 443, uuid: 00000000-0000-4000-8000-000000000002, cipher: auto, tls: true}\n")
}
