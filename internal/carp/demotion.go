package carp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

// SetDemotion adjusts demotion so the final kernel value equals the target.
// FreeBSD's net.inet.carp.demotion is ADDITIVE: the value written is added to
// the current factor. To reach an absolute target we must write:
//
//	delta = target - current
//
// Example: current=765, target=0 → write -765 → kernel subtracts 765 → 0.
func SetDemotion(target int) error {
	current, err := GetDemotion()
	if err != nil {
		// Fallback — write target as delta from 0
		return writeDemotion(target)
	}
	if current == target {
		return nil
	}
	return writeDemotion(target - current)
}

func writeDemotion(delta int) error {
	result := util.Exec("sysctl", fmt.Sprintf("net.inet.carp.demotion=%d", delta))
	if result.Err != nil {
		return fmt.Errorf("set demotion delta %d: %w (stderr: %s)", delta, result.Err, result.Stderr)
	}
	return nil
}

// GetPreempt reports net.inet.carp.preempt (0 = disabled, 1 = enabled).
// When enabled on a node, a BACKUP vhid preempts a master announcing itself
// with a higher advskew — i.e. the kernel performs the reclaim itself.
func GetPreempt() (int, error) {
	result := util.Exec("sysctl", "-n", "net.inet.carp.preempt")
	if result.Err != nil {
		return 0, fmt.Errorf("get preempt: %w (stderr: %s)", result.Err, result.Stderr)
	}
	return strconv.Atoi(strings.TrimSpace(result.Stdout))
}

func GetDemotion() (int, error) {
	result := util.Exec("sysctl", "-n", "net.inet.carp.demotion")
	if result.Err != nil {
		return 0, fmt.Errorf("get demotion: %w (stderr: %s)", result.Err, result.Stderr)
	}
	return strconv.Atoi(result.Stdout)
}
