package health

import (
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

const defaultProcessName = "dnsdist"

func CheckProcess(name string, timeout time.Duration) bool {
	if name == "" {
		name = defaultProcessName
	}
	result := util.ExecTimeout(timeout, "pgrep", "-x", name)
	return result.Err == nil
}

// CheckProcesses passes only when ALL given processes are alive.
func CheckProcesses(names []string, timeout time.Duration) bool {
	if len(names) == 0 {
		names = []string{defaultProcessName}
	}
	for _, n := range names {
		if !CheckProcess(n, timeout) {
			return false
		}
	}
	return true
}
