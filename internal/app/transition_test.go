package app

import (
	"errors"
	"strings"
	"testing"
)

func TestCoreCheckHeadroom(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"MemAvailable: 65536 kB\n", true},
		{"MemAvailable: 24888 kB\n", false},
		{"MemFree: 999999 kB\n", false},
		{"MemAvailable: -1 kB\n", false},
		{"MemAvailable: 65536 MB\n", false},
		{"", false},
	} {
		if got := coreCheckHeadroom([]byte(tc.text)); got != tc.want {
			t.Errorf("headroom(%q) = %v", tc.text, got)
		}
	}
}

func TestCoreTransitionOrderingAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   bool
		fail    string
		order   string
		message string
	}{
		{"parallel-capacity", true, "", "check,stop,start", ""},
		{"low-memory", false, "", "stop,check,start", ""},
		{"invalid-before-stop", true, "check", "check", "当前连接未改变"},
		{"invalid-after-stop", false, "check", "stop,check,restore", "已恢复原有状态"},
		{"stop-failure", false, "stop", "stop", "旧网络规则未能清理"},
		{"start-failure", false, "start", "stop,check,start,stop,restore", "start"},
		{"restore-failure", false, "check,restore", "stop,check,restore", "restore"},
		{"cleanup-failure", false, "start,stop2", "stop,check,start,stop", "规则清理未完成"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			stops := 0
			operation := func(name string) func() error {
				return func() error {
					calls = append(calls, name)
					key := name
					if name == "stop" {
						stops++
						if stops == 2 {
							key = "stop2"
						}
					}
					if strings.Contains(","+tc.fail+",", ","+key+",") {
						return errors.New(name)
					}
					return nil
				}
			}
			err := transitionCore(tc.first, operation("check"), operation("stop"), operation("start"), operation("restore"))
			if got := strings.Join(calls, ","); got != tc.order {
				t.Fatalf("order %s, want %s", got, tc.order)
			}
			if tc.message == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error %v, want %q", err, tc.message)
			}
		})
	}
}
