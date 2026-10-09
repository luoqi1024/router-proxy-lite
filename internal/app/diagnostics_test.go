package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticTailRetainsBoundedLastBytes(t *testing.T) {
	b := newDiagnosticTail(8)
	for _, input := range []string{"abc", "defghi", "0123456789"} {
		if n, err := b.Write([]byte(input)); n != len(input) || err != nil {
			t.Fatal(n, err)
		}
	}
	if got := string(b.Bytes()); got != "23456789" {
		t.Fatal(got)
	}
}

func TestDiagnosticsArePrivateBoundedAndSeparatedByState(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	d := &RouterDriver{DataDir: filepath.Join(t.TempDir(), "state")}
	for i := 0; i < 6; i++ {
		d.recordDiagnostic(fmt.Sprintf("event-%d", i), nil, bytes.Repeat([]byte("private-provider-secret\n"), 1000))
	}
	data, err := os.ReadFile(d.diagnosticPath())
	if err != nil || len(data) > 32<<10 || !strings.Contains(string(data), "event-5") {
		t.Fatal(len(data), err)
	}
	if other := (&RouterDriver{DataDir: "other"}).diagnosticPath(); other == d.diagnosticPath() {
		t.Fatal("diagnostics shared by independent state directories")
	}
}
