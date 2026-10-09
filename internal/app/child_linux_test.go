//go:build linux

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCoreRemainsAliveAcrossParentThreadChurnAndRecordsExit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	d := &RouterDriver{DataDir: t.TempDir()}
	output := newDiagnosticTail(1024)
	cmd := exec.Command("/bin/sh", "-c", "printf private-core-error >&2; exec sleep 30")
	configureChild(cmd)
	cmd.Stdout, cmd.Stderr = output, output
	p, err := d.startCore(cmd, output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-p.done })
	for i := 0; i < 12; i++ {
		done := make(chan struct{})
		go func() { runtime.LockOSThread(); close(done) }() // thread deliberately terminates
		<-done
		runtime.GC()
	}
	select {
	case <-p.done:
		t.Fatal("core died while its parent remained alive")
	default:
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("core wait did not complete")
	}
	log, err := os.ReadFile(d.diagnosticPath())
	if err != nil || !strings.Contains(string(log), "signal: terminated") || !strings.Contains(string(log), "private-core-error") {
		t.Fatal(string(log), err)
	}
	st, _ := os.Stat(d.diagnosticPath())
	if st.Mode().Perm() != 0600 {
		t.Fatal("diagnostic permissions", st.Mode())
	}
}

func TestNetworkTimeoutReleasesLockHeldByDescendants(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock unavailable")
	}
	dir := t.TempDir()
	lock := filepath.Join(dir, "lock")
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `exec 9>"$1"; flock -n 9; : >"$2"; sleep 30`, "sh", lock, ready)
	configureNetworkChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("holder did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := exec.Command("flock", "-n", lock, "true").Run(); err == nil {
		t.Fatal("expected held lock")
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("canceled network helper succeeded")
	}
	until = time.Now().Add(3 * time.Second)
	for {
		if exec.Command("flock", "-n", lock, "true").Run() == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("descendant retained flock after cancellation")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
