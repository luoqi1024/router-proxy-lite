package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type Driver interface {
	Apply(context.Context, State) error
	Running() bool
	Close() error
}
type DemoDriver struct {
	mu     sync.Mutex
	active bool
}

func (d *DemoDriver) Apply(_ context.Context, s State) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active = s.Enabled && s.Policy != "direct"
	return nil
}
func (d *DemoDriver) Running() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.active }
func (d *DemoDriver) Close() error  { d.mu.Lock(); defer d.mu.Unlock(); d.active = false; return nil }

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
}
type RouterDriver struct {
	mu                             sync.Mutex
	DataDir, Core, Assets, Scripts string
	Device                         Device
	current                        *child
	applied                        State
}

func (d *RouterDriver) network(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", filepath.Join(d.Scripts, "network.sh"), action)
	cmd.Env = append(os.Environ(), "RPL_LAN="+d.Device.LAN, "RPL_DATA="+d.DataDir)
	if err := cmd.Run(); err != nil {
		return errors.New("网络规则操作失败；请查看诊断记录")
	}
	return nil
}
func (d *RouterDriver) stopLocked() error {
	err := d.network("stop")
	p := d.current
	d.current = nil
	if p != nil {
		_ = p.cmd.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}
	return err
}
func (d *RouterDriver) launchLocked(ctx context.Context, s State) error {
	if !s.Enabled || s.Policy == "direct" {
		return nil
	}
	for _, check := range d.Device.Checks {
		if !check.OK {
			return fmt.Errorf("设备检查未通过：%s", check.Name)
		}
	}
	b, err := RenderConfig(s, d.Device, d.Assets)
	if err != nil {
		return err
	}
	// Apply validates once, serializing the check on memory-constrained devices.
	// The rollback configuration was validated previously.
	path := filepath.Join(d.DataDir, "config.runtime.json")
	if err = AtomicWrite(path, b); err != nil {
		return errors.New("保存运行配置失败")
	}
	cmd := exec.Command(d.Core, "run", "-c", path)
	configureChild(cmd)
	cmd.Env = coreRuntimeEnv(false)
	releaseCoreHeadroom()
	// Provider messages can include server addresses. Never expose raw core output in the UI.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return errors.New("代理内核启动失败")
	}
	p := &child{cmd: cmd, done: make(chan struct{})}
	d.current = p
	go func() {
		_ = cmd.Wait()
		close(p.done)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current == p {
			d.current = nil
			_ = d.network("stop")
		}
	}()
	ready := false
	for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
		select {
		case <-p.done:
			return errors.New("代理内核意外退出")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, e := net.DialTimeout("tcp", "127.0.0.1:17890", 200*time.Millisecond)
		if e == nil {
			conn.Close()
			if _, e = net.InterfaceByName("rpltun"); e == nil {
				ready = true
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ready {
		return errors.New("等待代理就绪超时")
	}
	if err := d.network("start"); err != nil {
		return err
	}
	go d.watch(p)
	return nil
}

// A firewall reload must not leave the UI claiming that protection is active.
// On loss of owned rules, stop interception and let the user explicitly retry.
func (d *RouterDriver) watch(p *child) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			d.mu.Lock()
			if d.current != p {
				d.mu.Unlock()
				return
			}
			if d.network("status") != nil {
				_ = d.stopLocked()
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
		}
	}
}
func (d *RouterDriver) Apply(ctx context.Context, s State) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	old := d.applied
	var config []byte
	if s.Enabled && s.Policy != "direct" {
		b, err := RenderConfig(s, d.Device, d.Assets)
		if err != nil {
			return err
		}
		config = b
	}
	check := func() error {
		if config == nil {
			return nil
		}
		tmp := filepath.Join(d.DataDir, "config.check.json")
		if err := AtomicWrite(tmp, config); err != nil {
			return err
		}
		defer os.Remove(tmp)
		cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, d.Core, "check", "-c", tmp)
		cmd.Env = coreRuntimeEnv(true)
		releaseCoreHeadroom()
		if err := cmd.Run(); err != nil {
			return errors.New("内核配置检查失败")
		}
		return nil
	}
	restore := func() error {
		rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if restoreErr := d.launchLocked(rctx, old); restoreErr != nil {
			d.stopLocked()
			return errors.New("新配置启动失败，旧代理也未能恢复；已尝试清理转发规则")
		}
		return nil
	}
	meminfo, _ := os.ReadFile("/proc/meminfo")
	if err := transitionCore(d.current == nil || coreCheckHeadroom(meminfo), check, d.stopLocked, func() error {
		return d.launchLocked(ctx, s)
	}, restore); err != nil {
		return err
	}
	d.applied = s
	return nil
}
func (d *RouterDriver) Running() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.current != nil }
func (d *RouterDriver) Close() error  { d.mu.Lock(); defer d.mu.Unlock(); return d.stopLocked() }

// Release transient API/configuration allocations before starting another Go
// runtime. The compact profile cannot afford to retain idle heap pages during
// the core's startup peak; its GOMEMLIMIT is a soft target, not an RSS limit.
func releaseCoreHeadroom() {
	if os.Getenv("RPL_MEMORY_PROFILE") == "compact" {
		debug.FreeOSMemory()
	}
}

func coreRuntimeEnv(check bool) []string {
	gc, limit, procs := "50", "32MiB", "2"
	if check {
		limit = "16MiB"
	}
	if os.Getenv("RPL_MEMORY_PROFILE") == "compact" {
		gc, limit, procs = "25", "12MiB", "1"
	}
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GOGC" && name != "GOMEMLIMIT" && name != "GOMAXPROCS" {
			env = append(env, entry)
		}
	}
	return append(env, "GOGC="+gc, "GOMEMLIMIT="+limit, "GOMAXPROCS="+procs)
}

func DemoDevice() Device {
	return Device{Mode: "demo", Architecture: runtime.GOARCH, LAN: "演示局域网", WAN: "演示上联网口", LANAddress: "192.168.31.1", Checks: []Check{{"电脑演示模式", true, "不会启动内核或修改任何网络规则"}}}
}
func ProbeRouter(lan, wan, core, assets string) Device {
	d := Device{Mode: "router", Architecture: runtime.GOARCH, LAN: lan, WAN: wan}
	add := func(n string, ok bool, detail string) { d.Checks = append(d.Checks, Check{n, ok, detail}) }
	add("系统", runtime.GOOS == "linux", "第一版仅为 Linux / BusyBox 路由器准备，尚待实机验证")
	add("管理员权限", os.Geteuid() == 0, "需要 root 权限")
	_, err := os.Stat("/dev/net/tun")
	add("TUN", err == nil, "需要 /dev/net/tun")
	for _, name := range []string{"ip", "iptables"} {
		_, err = exec.LookPath(name)
		add(name, err == nil, "需要系统已有的网络命令")
	}
	if wan == "" {
		out, e := exec.Command("ip", "-4", "route", "show", "default").Output()
		if e == nil {
			fields := strings.Fields(string(out))
			for i, f := range fields {
				if f == "dev" && i+1 < len(fields) {
					wan = fields[i+1]
					break
				}
			}
		}
	}
	if lan == "" {
		for _, candidate := range []string{"br-lan", "br0"} {
			if _, e := net.InterfaceByName(candidate); e == nil && candidate != wan {
				lan = candidate
				break
			}
		}
	}
	d.LAN = lan
	d.WAN = wan
	for _, name := range []string{lan, wan} {
		_, e := net.InterfaceByName(name)
		add("接口 "+name, e == nil && name != "", "LAN 与 WAN 必须独立且能够识别")
	}
	add("接口隔离", lan != "" && wan != "" && lan != wan, "不在上联网口接管流量")
	ipv6 := false
	if iface, e := net.InterfaceByName(lan); e == nil {
		if addresses, e := iface.Addrs(); e == nil {
			for _, a := range addresses {
				ip, _, e := net.ParseCIDR(a.String())
				if e != nil {
					continue
				}
				if ip.To4() != nil {
					d.LANAddress = ip.String()
				} else if ip.IsGlobalUnicast() {
					ipv6 = true
				}
			}
		}
	}
	add("LAN IPv4", d.LANAddress != "", "需要局域网 IPv4 地址")
	add("IPv6 范围", !ipv6, "首版只支持 IPv4；发现 LAN IPv6 时拒绝启用，避免流量绕过")
	for _, p := range []string{core, filepath.Join(assets, "ca-certificates.crt"), filepath.Join(assets, "geoip-cn.srs"), filepath.Join(assets, "geosite-cn.srs")} {
		st, e := os.Stat(p)
		add(filepath.Base(p), e == nil && st != nil && !st.IsDir(), "需要已校验的内核与资源文件")
	}
	return d
}
