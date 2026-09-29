package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"router-proxy-lite/internal/app"
)

func main() {
	mode := flag.String("mode", "demo", "demo or router; router mode is experimental")
	listen := flag.String("listen", "127.0.0.1:8787", "HTTP address; router mode must bind exactly to LAN IP")
	dir := flag.String("data-dir", ".local/demo", "private state directory")
	core := flag.String("core", "/data/routerlite/bin/sing-box", "sing-box executable")
	assets := flag.String("assets", "/data/routerlite/assets", "CA and rule sets directory")
	scripts := flag.String("scripts", "/data/routerlite/scripts", "network helper directory")
	lan := flag.String("lan", "", "LAN interface; detected when omitted")
	wan := flag.String("wan", "", "WAN interface; detected when omitted")
	showVersion := flag.Bool("version", false, "print version")
	inspect := flag.Bool("check", false, "read-only device checks; never modify network")
	allowExperimental := flag.Bool("experimental-router", false, "acknowledge untested router integration")
	flag.Parse()
	if *showVersion {
		fmt.Println("RouterLite", app.Version, runtime.GOOS, runtime.GOARCH)
		return
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatal(err)
	}
	*dir = abs
	device := app.DemoDevice()
	var driver app.Driver = &app.DemoDriver{}
	if *mode == "router" {
		if runtime.GOOS != "linux" {
			log.Fatal("router mode requires Linux")
		}
		device = app.ProbeRouter(*lan, *wan, *core, *assets)
		if os.Getenv("SSL_CERT_FILE") == "" {
			_ = os.Setenv("SSL_CERT_FILE", filepath.Join(*assets, "ca-certificates.crt"))
		}
		if !*inspect && !*allowExperimental {
			log.Fatal("Router integration has not passed hardware tests. Use --check first; experimental mode requires --experimental-router.")
		}
		host, _, e := net.SplitHostPort(*listen)
		if !*inspect && (e != nil || host != device.LANAddress || host == "") {
			log.Fatal("HTTP must bind exactly to the detected LAN IPv4 address")
		}
		driver = &app.RouterDriver{DataDir: *dir, Core: *core, Assets: *assets, Scripts: *scripts, Device: device}
	} else if *mode != "demo" {
		log.Fatal("unknown mode")
	}
	if *inspect {
		ok := true
		for _, c := range device.Checks {
			fmt.Printf("%t  %s: %s\n", c.OK, c.Name, c.Detail)
			ok = ok && c.OK
		}
		if !ok {
			os.Exit(1)
		}
		return
	}
	if *mode == "demo" {
		host, _, e := net.SplitHostPort(*listen)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			log.Fatal("demo mode may only bind to loopback")
		}
	}
	s, key, err := app.NewServer(*dir, *mode, driver, device)
	if err != nil {
		log.Fatal(err)
	}
	if key != "" {
		fmt.Println("Initial management key:", key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	if err = s.Resume(ctx); err != nil {
		log.Print("Autostart did not complete; use the authenticated status page for details.")
	}
	cancel()
	s.StartMonitoring()
	server := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, terminationSignal())
	go func() {
		<-signals
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = s.Close()
	}()
	fmt.Printf("RouterLite %s (%s) http://%s\n", app.Version, *mode, *listen)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		_ = s.Close()
		log.Fatal(err)
	}
	_ = s.Close()
}
