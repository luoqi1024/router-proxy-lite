//go:build !windows

package main

import (
	"os"
	"syscall"
)

func terminationSignal() os.Signal { return syscall.SIGTERM }
