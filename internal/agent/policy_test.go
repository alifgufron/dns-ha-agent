package agent

import (
	"testing"

	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

func TestStateFromScore(t *testing.T) {
	cases := []struct {
		score int
		want  State
	}{
		{100, StateHealthy},
		{80, StateHealthy},
		{79, StateDegraded},
		{40, StateDegraded},
		{39, StateUnhealthy},
		{0, StateUnhealthy},
	}
	for _, tc := range cases {
		if got := StateFromScore(tc.score); got != tc.want {
			t.Errorf("score %d: got %v, want %v", tc.score, got, tc.want)
		}
	}
}

func TestTransitioned(t *testing.T) {
	if StateHealthy.Transitioned(StateHealthy) {
		t.Error("same state should not transition")
	}
	if !StateHealthy.Transitioned(StateUnhealthy) {
		t.Error("different state should transition")
	}
}

func healthyPeer(score int) peer.PeerHealth {
	return peer.PeerHealth{Name: "p", Score: score, OK: true}
}

// testDemotion mirrors the documented defaults (carp.demotion_*).
var testDemotion = DemotionLevels{Healthy: 0, Degraded: 50, Unhealthy: 255}

// Custom carp.demotion_* values must be honored, not hardcoded.
func TestEvaluatePolicyUsesConfiguredDemotion(t *testing.T) {
	custom := DemotionLevels{Healthy: 5, Degraded: 77, Unhealthy: 200}

	if d := EvaluatePolicy(PolicyPreempt, 10, carp.StateMaster, nil, custom); d.DesiredDemotion != 200 {
		t.Errorf("unhealthy demotion = %d, want 200", d.DesiredDemotion)
	}
	if d := EvaluatePolicy(PolicyPreempt, 50, carp.StateMaster, nil, custom); d.DesiredDemotion != 77 {
		t.Errorf("degraded demotion = %d, want 77", d.DesiredDemotion)
	}
	if d := EvaluatePolicy(PolicyPreempt, 100, carp.StateMaster, nil, custom); d.DesiredDemotion != 5 {
		t.Errorf("healthy demotion = %d, want 5", d.DesiredDemotion)
	}
}

func TestEvaluatePolicyUnhealthy(t *testing.T) {
	d := EvaluatePolicy(PolicyPreempt, 30, carp.StateMaster, nil, testDemotion)
	if !d.DesiredIfaceDown || d.DesiredDemotion != 255 {
		t.Errorf("unhealthy should demote+down, got %+v", d)
	}
}

func TestEvaluatePolicyDegraded(t *testing.T) {
	d := EvaluatePolicy(PolicyPreempt, 50, carp.StateMaster, nil, testDemotion)
	if d.DesiredIfaceDown || d.DesiredDemotion != 50 {
		t.Errorf("degraded should demote 50 up, got %+v", d)
	}
}

func TestEvaluatePolicySticky(t *testing.T) {
	// Sticky stays MASTER even when a peer is healthier.
	d := EvaluatePolicy(PolicySticky, 100, carp.StateMaster, []peer.PeerHealth{healthyPeer(100)}, testDemotion)
	if d.DesiredDemotion != 0 || d.DesiredIfaceDown {
		t.Errorf("sticky healthy should stay, got %+v", d)
	}
}

func TestEvaluatePolicyPreemptStepDown(t *testing.T) {
	// Healthy MASTER + healthier peer (higher score) → step down with demotion 50.
	d := EvaluatePolicy(PolicyPreempt, 90, carp.StateMaster, []peer.PeerHealth{healthyPeer(95)}, testDemotion)
	if d.DesiredDemotion != 50 || d.DesiredIfaceDown {
		t.Errorf("preempt should demote 50 when healthier peer exists, got %+v", d)
	}
}

func TestEvaluatePolicyPreemptStay(t *testing.T) {
	// Healthy MASTER, no healthier peer → stay.
	d := EvaluatePolicy(PolicyPreempt, 90, carp.StateMaster, []peer.PeerHealth{healthyPeer(80)}, testDemotion)
	if d.DesiredDemotion != 0 {
		t.Errorf("preempt should stay at demotion 0, got %+v", d)
	}
}

func TestParsePolicyMode(t *testing.T) {
	if ParsePolicyMode("preempt") != PolicyPreempt {
		t.Error("preempt should parse to PolicyPreempt")
	}
	if ParsePolicyMode("sticky") != PolicySticky {
		t.Error("sticky should parse to PolicySticky")
	}
	if ParsePolicyMode("") != PolicySticky {
		t.Error("empty should default to PolicySticky")
	}
}
