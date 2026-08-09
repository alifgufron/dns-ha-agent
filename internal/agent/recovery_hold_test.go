package agent

import (
	"testing"

	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

const (
	testConfirm = 5
	testMax     = 100
)

// healthyDecision is what EvaluatePolicy returns for a healthy preempt node:
// interface up, demotion 0 — i.e. "reclaim the VIP now".
func healthyDecision() PolicyDecision {
	return EvaluatePolicy(PolicyPreempt, 100, carp.StateBackup, []peer.PeerHealth{}, testDemotion)
}

// The reported bug: a node whose process had just restarted (score 25) took
// MASTER back immediately because a single check passed.
func TestRecoveryHoldBlocksPartiallyHealthyNode(t *testing.T) {
	d, streak, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, 25, testMax, testConfirm, 0, testDemotion)

	if !held {
		t.Fatal("a node at score 25/100 must not reclaim the VIP")
	}
	if !d.DesiredIfaceDown {
		t.Error("held node must keep its VIP interface down")
	}
	if d.DesiredDemotion != testDemotion.Unhealthy {
		t.Errorf("held node must keep demotion %d so peers do not step down for it, got %d",
			testDemotion.Unhealthy, d.DesiredDemotion)
	}
	if streak != 0 {
		t.Errorf("streak must stay 0 while not fully healthy, got %d", streak)
	}
}

func TestRecoveryHoldReleasesAfterConfirmations(t *testing.T) {
	streak := 0
	for i := 1; i < testConfirm; i++ {
		var held bool
		_, streak, held = applyRecoveryHold(
			PolicyPreempt, healthyDecision(), true, testMax, testMax, testConfirm, streak, testDemotion)
		if !held {
			t.Fatalf("released after only %d/%d stable checks", i, testConfirm)
		}
		if streak != i {
			t.Fatalf("streak = %d, want %d", streak, i)
		}
	}

	d, _, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, testMax, testMax, testConfirm, streak, testDemotion)
	if held {
		t.Fatalf("must reclaim after %d consecutive fully-healthy checks", testConfirm)
	}
	if d.DesiredIfaceDown {
		t.Error("released node must bring its VIP interface up")
	}
	if d.DesiredDemotion != testDemotion.Healthy {
		t.Errorf("released node demotion = %d, want %d", d.DesiredDemotion, testDemotion.Healthy)
	}
}

// A flaky node (the DEGRADED/HEALTHY flapping in the report) must restart its
// streak, otherwise it creeps up to the threshold while still unstable.
func TestRecoveryHoldResetsStreakOnAnyFailedCheck(t *testing.T) {
	_, streak, _ := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, testMax, testMax, testConfirm, 0, testDemotion)
	_, streak, _ = applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, testMax, testMax, testConfirm, streak, testDemotion)
	if streak != 2 {
		t.Fatalf("setup: streak = %d, want 2", streak)
	}

	// One DNS check fails -> 75/100.
	_, streak, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, 75, testMax, testConfirm, streak, testDemotion)
	if streak != 0 {
		t.Errorf("streak must reset on a partial score, got %d", streak)
	}
	if !held {
		t.Error("node must stay held after a failed check")
	}
}

// Sticky never reclaims, so gating it would only delay it rejoining as BACKUP.
func TestRecoveryHoldSkipsStickyMode(t *testing.T) {
	d := EvaluatePolicy(PolicySticky, 100, carp.StateBackup, []peer.PeerHealth{}, testDemotion)
	got, _, held := applyRecoveryHold(
		PolicySticky, d, true, 25, testMax, testConfirm, 0, testDemotion)
	if held || got.DesiredIfaceDown {
		t.Error("sticky mode must not be held back")
	}
}

// A node that never went down is not recovering; it must not be touched.
func TestRecoveryHoldSkipsNodeThatWasUp(t *testing.T) {
	_, _, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), false, 25, testMax, testConfirm, 0, testDemotion)
	if held {
		t.Error("a node whose interface was already up must not be held")
	}
}

// Staying down (unhealthy decision) is not a reclaim and must pass through, so
// failover is never delayed by this gate.
func TestRecoveryHoldDoesNotDelayFailover(t *testing.T) {
	d := EvaluatePolicy(PolicyPreempt, 0, carp.StateMaster, []peer.PeerHealth{}, testDemotion)
	got, _, held := applyRecoveryHold(
		PolicyPreempt, d, true, 0, testMax, testConfirm, 0, testDemotion)
	if held {
		t.Error("an unhealthy decision must pass through untouched")
	}
	if !got.DesiredIfaceDown {
		t.Error("unhealthy node must stay down")
	}
}

func TestRecoveryHoldDisabledWhenConfirmZero(t *testing.T) {
	_, _, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, 25, testMax, 0, 0, testDemotion)
	if held {
		t.Error("confirm <= 0 must disable the gate")
	}
}

// With a check disabled the maximum drops below 100, so "fully healthy" must
// track max_score rather than a hardcoded 100.
func TestRecoveryHoldUsesMaxScoreNotHundred(t *testing.T) {
	const maxWithoutDNS = 75
	_, streak, held := applyRecoveryHold(
		PolicyPreempt, healthyDecision(), true, maxWithoutDNS, maxWithoutDNS, testConfirm, 0, testDemotion)
	if streak != 1 {
		t.Errorf("score equal to max must count as fully healthy, streak = %d", streak)
	}
	if !held {
		t.Error("still held until the streak is met")
	}
}
