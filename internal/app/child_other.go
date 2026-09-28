//go:build !linux

package app

import "os/exec"

func configureChild(cmd *exec.Cmd) {}
