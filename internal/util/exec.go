package util

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type ExecResult struct {
	Stdout string
	Stderr string
	Err    error
}

func ExecTimeout(timeout time.Duration, name string, args ...string) ExecResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("command %q timed out after %v: %w", name, timeout, ctx.Err())
	}

	return ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

func Exec(name string, args ...string) ExecResult {
	return ExecTimeout(10*time.Second, name, args...)
}

// PingHost returns whether an ICMP echo reply was received plus a short detail
// string: the round-trip time on success, or the ping error otherwise. The
// detail gives an operator the "why" behind a bare true/false — a timeout, an
// RTO, or a slow reply all look identical otherwise.
// IPv4 → ping, IPv6 → ping6 (FreeBSD).
func PingHost(ip string, timeout time.Duration) (bool, string) {
	pinger := "ping"
	args := []string{"-t", "1", "-c", "1", ip}
	if isIPv6(ip) {
		pinger = "ping6"
		args = []string{"-c", "1", ip}
	}
	result := ExecTimeout(timeout, pinger, args...)
	if result.Err != nil {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = result.Err.Error()
		}
		return false, detail
	}
	return true, pingRTT(result.Stdout)
}

// pingTimeRe matches the per-reply round-trip line, e.g. "time=0.458 ms".
var pingTimeRe = regexp.MustCompile(`time=([0-9.]+) ms`)

// pingRTT extracts the round-trip time from ping's output.
func pingRTT(out string) string {
	m := pingTimeRe.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1] + " ms"
}

func isIPv6(ip string) bool {
	for i := 0; i < len(ip); i++ {
		if ip[i] == ':' {
			return true
		}
	}
	return false
}
