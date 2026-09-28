"""Apply the documented minimal registry to a pristine sing-box v1.14.2 tree.

No protocol/cryptographic implementation is changed. Keep the upstream LICENSE.
"""
from pathlib import Path
import sys

root = Path(sys.argv[1]).resolve()
assert 'module github.com/sagernet/sing-box' in (root / 'go.mod').read_text()
imports = ['adapter/certificate', 'adapter/endpoint', 'adapter/inbound',
           'adapter/outbound', 'adapter/service', 'dns', 'dns/transport',
           'dns/transport/local', 'protocol/direct', 'protocol/mixed',
           'protocol/tun', 'protocol/anytls', 'protocol/shadowsocks',
           'protocol/trojan', 'protocol/vless', 'protocol/vmess']
code = '''// RouterLite minimal registry, modified 2026-09-28.
// Based on sing-box v1.14.2, copyright nekohasekai, GPL-3.0-or-later.
package include
import (
 "context"
 box "github.com/sagernet/sing-box"
'''
code += ''.join(f' "github.com/sagernet/sing-box/{p}"\n' for p in imports)
code += ''')
func Context(ctx context.Context) context.Context {
 return box.Context(ctx, InboundRegistry(), OutboundRegistry(), EndpointRegistry(), DNSTransportRegistry(), ServiceRegistry(), CertificateProviderRegistry())
}
func InboundRegistry() *inbound.Registry {
 r:=inbound.NewRegistry();tun.RegisterInbound(r);direct.RegisterInbound(r);mixed.RegisterInbound(r);return r
}
func OutboundRegistry() *outbound.Registry {
 r:=outbound.NewRegistry();direct.RegisterOutbound(r);anytls.RegisterOutbound(r);shadowsocks.RegisterOutbound(r);trojan.RegisterOutbound(r);vless.RegisterOutbound(r);vmess.RegisterOutbound(r);return r
}
func EndpointRegistry() *endpoint.Registry {return endpoint.NewRegistry()}
func DNSTransportRegistry() *dns.TransportRegistry {
 r:=dns.NewTransportRegistry();transport.RegisterTCP(r);transport.RegisterUDP(r);local.RegisterTransport(r);return r
}
func ServiceRegistry() *service.Registry {return service.NewRegistry()}
func CertificateProviderRegistry() *certificate.Registry {return certificate.NewRegistry()}
'''
(root / 'include/registry.go').write_text(code, encoding='utf-8', newline='\n')
