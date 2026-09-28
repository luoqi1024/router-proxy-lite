//go:build windows

package main

import "os"

func terminationSignal() os.Signal { return os.Interrupt }
