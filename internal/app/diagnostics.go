package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Provider errors may contain credentials. Keep only a bounded private tail,
// never expose this buffer through View, events or HTTP error responses.
type diagnosticTail struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newDiagnosticTail(limit int) *diagnosticTail { return &diagnosticTail{limit: limit} }
func (b *diagnosticTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n >= b.limit {
		b.data = append(b.data[:0], p[n-b.limit:]...)
	} else {
		if overflow := len(b.data) + n - b.limit; overflow > 0 {
			copy(b.data, b.data[overflow:])
			b.data = b.data[:len(b.data)-overflow]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *diagnosticTail) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

var diagnosticsMu sync.Mutex

func (d *RouterDriver) diagnosticPath() string {
	hash := sha256.Sum256([]byte(d.DataDir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("routerlite-diagnostics-%x", hash[:5]), "events.log")
}

func (d *RouterDriver) recordDiagnostic(event string, err error, output []byte) {
	diagnosticsMu.Lock()
	defer diagnosticsMu.Unlock()
	path := d.diagnosticPath()
	if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.Chmod(filepath.Dir(path), 0700) != nil {
		return
	}
	entry := fmt.Sprintf("\n%s %s result=%v\n", time.Now().UTC().Format(time.RFC3339Nano), event, err)
	if memory, e := os.ReadFile("/proc/meminfo"); e == nil {
		for _, line := range strings.Split(string(memory), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") || strings.HasPrefix(line, "MemFree:") {
				entry += line + "\n"
			}
		}
	}
	entry += string(output)
	const limit = 32 << 10
	var previous []byte
	if st, e := os.Lstat(path); e == nil && st.Mode().IsRegular() && st.Size() <= limit {
		previous, _ = os.ReadFile(path)
	}
	data := append(previous, []byte(entry)...)
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	// tmpfs only, 0600 atomic replacement; no persistent flash log growth.
	_ = AtomicWrite(path, data)
}

func (d *RouterDriver) startCore(cmd *exec.Cmd, output *diagnosticTail) (*child, error) {
	p := &child{cmd: cmd, done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		// Linux Pdeathsig follows the creating OS thread, not just the process.
		// Keep it alive until Wait completes, including Go runtime thread churn.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			d.recordDiagnostic("core start", err, output.Bytes())
			started <- err
			close(p.done)
			return
		}
		d.recordDiagnostic(fmt.Sprintf("core started pid=%d", cmd.Process.Pid), nil, nil)
		started <- nil
		err := cmd.Wait()
		d.recordDiagnostic(fmt.Sprintf("core exited pid=%d", cmd.Process.Pid), err, output.Bytes())
		close(p.done)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current == p {
			d.current = nil
			_ = d.network("stop") // network() records any cleanup error privately.
		}
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}
