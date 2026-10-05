package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

func RenderConfig(s State, d Device, assets string) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	n, err := s.Node()
	if err != nil {
		return nil, err
	}
	rules := []any{map[string]any{"inbound": []string{"dns-in"}, "action": "hijack-dns"}, map[string]any{"port": 53, "action": "hijack-dns"}, map[string]any{"action": "sniff", "timeout": "300ms"}, map[string]any{"ip_is_private": true, "action": "route", "outbound": "direct"}}
	dnsRules := []any{map[string]any{"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR"}}
	if s.Policy == "rule" {
		rules = append(rules, map[string]any{"domain_suffix": []string{"cn", "lan"}, "action": "route", "outbound": "direct"}, map[string]any{"rule_set": []string{"geosite-cn", "geoip-cn"}, "action": "route", "outbound": "direct"})
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": []string{"cn", "lan"}, "action": "route", "server": "local-dns"}, map[string]any{"rule_set": []string{"geosite-cn"}, "action": "route", "server": "local-dns"})
	}
	sets := []any{}
	for _, name := range []string{"geoip-cn", "geosite-cn"} {
		sets = append(sets, map[string]any{"type": "local", "tag": name, "format": "binary", "path": filepath.Join(assets, name+".srs")})
	}
	config := map[string]any{
		"log":         map[string]any{"level": "warn", "timestamp": true},
		"certificate": map[string]any{"certificate_path": []string{filepath.Join(assets, "ca-certificates.crt")}},
		"dns":         map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "local-dns", "server": "223.5.5.5"}, map[string]any{"type": "https", "tag": "proxy-dns", "server": "1.1.1.1", "server_port": 443, "path": "/dns-query", "tls": map[string]any{"enabled": true, "server_name": "cloudflare-dns.com"}, "detour": "proxy"}}, "rules": dnsRules, "final": "proxy-dns", "strategy": "ipv4_only", "cache_capacity": 512, "reverse_mapping": true},
		"inbounds": []any{
			map[string]any{"type": "mixed", "tag": "health", "listen": "127.0.0.1", "listen_port": 17890},
			map[string]any{"type": "direct", "tag": "dns-in", "listen": d.LANAddress, "listen_port": 1053},
			map[string]any{"type": "tun", "tag": "lan-tun", "interface_name": "rpltun", "address": []string{"172.29.180.1/30"}, "mtu": 1400, "auto_route": false, "auto_redirect": false, "dns_mode": "disabled", "stack": "system"},
		},
		"outbounds": []any{n.Outbound, map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"default_domain_resolver": map[string]any{"server": "local-dns", "strategy": "ipv4_only"}, "default_interface": d.WAN, "rules": rules, "rule_set": sets, "final": "proxy"},
	}
	if s.Failover.Enabled {
		outbounds := config["outbounds"].([]any)
		inbounds := config["inbounds"].([]any)
		probeRules := []any{}
		addProbe := func(index int, outbound string) {
			tag := fmt.Sprintf("probe-%d", index)
			inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": probePort + index})
			probeRules = append(probeRules, map[string]any{"inbound": []string{tag}, "action": "route", "outbound": outbound})
		}
		addProbe(0, "proxy")
		for i, id := range s.Failover.Nodes {
			tag := "proxy"
			if id != s.Selected {
				for _, candidate := range s.Subscription.Nodes {
					if candidate.ID != id {
						continue
					}
					tag = fmt.Sprintf("backup-%d", i)
					outbound := make(map[string]any, len(candidate.Outbound))
					for k, v := range candidate.Outbound {
						outbound[k] = v
					}
					outbound["tag"] = tag
					outbounds = append(outbounds, outbound)
					break
				}
			}
			addProbe(i+1, tag)
		}
		config["outbounds"] = outbounds
		config["inbounds"] = inbounds
		config["route"].(map[string]any)["rules"] = append(probeRules, rules...)
	}
	return json.MarshalIndent(config, "", "  ")
}
