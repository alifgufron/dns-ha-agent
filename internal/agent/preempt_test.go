package agent

import (
	"testing"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

// shouldDeferToKernel mirrors the decision made in runOnce step 2: when the
// higher-priority peer runs net.inet.carp.preempt=1, the agent waits for the
// kernel to reclaim instead of stepping down itself (avoids duplicate action),
// but falls back after kernelPreemptGrace so a reclaim is still guaranteed.
func shouldDeferToKernel(p peer.PeerHealth, waitingSince time.Time, now time.Time) bool {
	if p.Preempt != 1 {
		return false
	}
	if waitingSince.IsZero() {
		return true // first cycle — start the grace timer
	}
	return now.Sub(waitingSince) < kernelPreemptGrace
}

func peerWithPreempt(preempt int) peer.PeerHealth {
	return peer.PeerHealth{Name: "node-a", Score: 100, OK: true, Advskew: 0, Preempt: preempt}
}

// Peer without kernel preempt: the agent must act immediately, otherwise
// nothing would ever reclaim the VIP.
func TestNoKernelPreemptStepsDownImmediately(t *testing.T) {
	if shouldDeferToKernel(peerWithPreempt(0), time.Time{}, time.Now()) {
		t.Error("must not defer when peer has net.inet.carp.preempt=0")
	}
}

// Peer with kernel preempt: defer so both mechanisms don't fire at once.
func TestKernelPreemptDefersFirst(t *testing.T) {
	if !shouldDeferToKernel(peerWithPreempt(1), time.Time{}, time.Now()) {
		t.Error("must defer to kernel on the first cycle")
	}
	now := time.Now()
	if !shouldDeferToKernel(peerWithPreempt(1), now.Add(-5*time.Second), now) {
		t.Error("must still defer within the grace period")
	}
}

// Kernel preempt enabled but it never reclaimed — the agent must take over,
// otherwise a failed sysctl preempt would strand the VIP on the wrong node.
func TestKernelPreemptFallbackAfterGrace(t *testing.T) {
	now := time.Now()
	stale := now.Add(-(kernelPreemptGrace + time.Second))
	if shouldDeferToKernel(peerWithPreempt(1), stale, now) {
		t.Error("must fall back to agent step-down after the grace period")
	}
}

func TestKernelPreemptGraceExceedsCarpTakeover(t *testing.T) {
	// CARP takeover is ~3 × advbase (~3s); the grace must comfortably exceed it.
	if kernelPreemptGrace < 10*time.Second {
		t.Errorf("kernelPreemptGrace = %v, too tight for CARP takeover", kernelPreemptGrace)
	}
}
