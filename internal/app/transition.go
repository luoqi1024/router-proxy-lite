package app

import (
	"errors"
	"strconv"
	"strings"
)

// A packed ARM core needs transient memory to unpack and check its configuration.
// This is a conservative heuristic, not a guarantee against every allocation failure.
func coreCheckHeadroom(meminfo []byte) bool {
	for _, line := range strings.Split(string(meminfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "MemAvailable:" && fields[2] == "kB" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			return err == nil && kb >= 65536
		}
	}
	return false
}

// Check before interrupting traffic when there is room for both processes.
// Otherwise stop first and restore the known-good state if validation fails.
func transitionCore(checkFirst bool, check, stop, start, restore func() error) error {
	if checkFirst {
		if err := check(); err != nil {
			return errors.New("新配置检查失败，当前连接未改变")
		}
	}
	if err := stop(); err != nil {
		return errors.New("旧网络规则未能清理，请先运行维护脚本 stop")
	}
	if !checkFirst {
		if err := check(); err != nil {
			if e := restore(); e != nil {
				return e
			}
			return errors.New("新配置检查失败，已恢复原有状态")
		}
	}
	if err := start(); err != nil {
		if e := stop(); e != nil {
			return errors.New("启动失败且规则清理未完成，请运行维护脚本 stop")
		}
		if e := restore(); e != nil {
			return e
		}
		return err
	}
	return nil
}
