package app

import (
	"strings"
	"testing"
)

func TestCoreRuntimeEnvironmentOverridesParentUniquely(t *testing.T) {
	t.Setenv("GOGC", "99")
	t.Setenv("GOMEMLIMIT", "24MiB")
	t.Setenv("GOMAXPROCS", "9")
	t.Setenv("SSL_CERT_FILE", "/test/ca.crt")
	for _, tc := range []struct {
		profile          string
		check            bool
		limit, gc, procs string
	}{
		{"standard", false, "32MiB", "50", "2"},
		{"standard", true, "16MiB", "50", "2"},
		{"compact", false, "12MiB", "25", "1"},
		{"compact", true, "12MiB", "25", "1"},
	} {
		t.Setenv("RPL_MEMORY_PROFILE", tc.profile)
		values, counts := map[string]string{}, map[string]int{}
		for _, e := range coreRuntimeEnv(tc.check) {
			k, v, _ := strings.Cut(e, "=")
			values[k] = v
			counts[k]++
		}
		for k, want := range map[string]string{"GOGC": tc.gc, "GOMEMLIMIT": tc.limit, "GOMAXPROCS": tc.procs, "SSL_CERT_FILE": "/test/ca.crt"} {
			if values[k] != want || counts[k] != 1 {
				t.Fatalf("%s: got %q (%d entries), want %q", k, values[k], counts[k], want)
			}
		}
	}
}
