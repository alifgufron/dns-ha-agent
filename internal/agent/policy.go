package agent

import (
	"fmt"

	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

type PolicyMode int

const (
	PolicySticky PolicyMode = iota
	PolicyPreempt
)

// DemotionLevels holds the configured demotion value per health state
// (carp.demotion_* in the config file).
type DemotionLevels struct {
	Healthy   int
	Degraded  int
	Unhealthy int
}

type PolicyDecision struct {
	DesiredDemotion  int
	DesiredIfaceDown bool
	Action           string
}

func EvaluatePolicy(mode PolicyMode, score int, carpState carp.State, peerHealths []peer.PeerHealth, demotion DemotionLevels) PolicyDecision {
	state := StateFromScore(score)

	if state == StateUnhealthy {
		return PolicyDecision{
			DesiredDemotion:  demotion.Unhealthy,
			DesiredIfaceDown: true,
			Action:           fmt.Sprintf("unhealthy — demotion %d, vip_iface down", demotion.Unhealthy),
		}
	}

	if state == StateDegraded {
		return PolicyDecision{
			DesiredDemotion:  demotion.Degraded,
			DesiredIfaceDown: false,
			Action:           fmt.Sprintf("degraded — demotion %d, vip_iface up", demotion.Degraded),
		}
	}

	// HEALTHY state
	switch mode {
	case PolicySticky:
		return PolicyDecision{
			DesiredDemotion:  demotion.Healthy,
			DesiredIfaceDown: false,
			Action:           fmt.Sprintf("healthy/sticky — demotion %d, vip_iface up", demotion.Healthy),
		}

	case PolicyPreempt:
		hasHealthierPeer := false
		for _, ph := range peerHealths {
			if ph.OK && ph.Score > score {
				hasHealthierPeer = true
				break
			}
		}

		if hasHealthierPeer && carpState == carp.StateMaster {
			return PolicyDecision{
				DesiredDemotion:  demotion.Degraded,
				DesiredIfaceDown: false,
				Action:           fmt.Sprintf("preempt — healthier peer exists, demotion %d", demotion.Degraded),
			}
		}

		return PolicyDecision{
			DesiredDemotion:  demotion.Healthy,
			DesiredIfaceDown: false,
			Action:           fmt.Sprintf("healthy/preempt — demotion %d, vip_iface up", demotion.Healthy),
		}
	}

	return PolicyDecision{
		DesiredDemotion:  demotion.Healthy,
		DesiredIfaceDown: false,
		Action:           fmt.Sprintf("default — demotion %d, vip_iface up", demotion.Healthy),
	}
}

// DefaultRecoveryConfirm is used when agent.recovery_confirm is unset.
const DefaultRecoveryConfirm = 3

// applyRecoveryHold delays reclaiming the VIP until a node that had its VIP
// interface down has been FULLY healthy (every enabled check passing, i.e.
// raw == max) for `confirm` consecutive intervals. It returns the decision to
// act on, the updated streak, and whether the node is still being held back.
//
// Without this, a single passing check is enough to bring the interface up, and
// a kernel with net.inet.carp.preempt=1 (or plain CARP priority) hands the VIP
// straight back to a node whose DNS is only half up — observed as a node
// retaking MASTER at score 25 and then flapping DEGRADED/HEALTHY.
//
// While held, the node keeps the full unhealthy posture: interface down AND
// demotion at the unhealthy level. Holding the demotion matters as much as the
// interface — peers decide whether to step down by comparing effective advskew
// (advskew + demotion), so a held node that advertised demotion 0 would make a
// healthy MASTER stand down for a node that is not ready to serve, leaving the
// VIP briefly owned by nobody.
//
// Only preempt mode is gated. Sticky never reclaims, so holding its interface
// down would just delay it rejoining as BACKUP for no benefit.
func applyRecoveryHold(mode PolicyMode, decision PolicyDecision, ifaceWasDown bool, rawScore, maxScore, confirm, streak int, demotion DemotionLevels) (PolicyDecision, int, bool) {
	reclaiming := ifaceWasDown && !decision.DesiredIfaceDown
	if confirm <= 0 || mode != PolicyPreempt || !reclaiming {
		return decision, 0, false
	}

	if maxScore > 0 && rawScore >= maxScore {
		streak++
		if streak >= confirm {
			return decision, streak, false
		}
	} else {
		streak = 0
	}

	return PolicyDecision{
		DesiredDemotion:  demotion.Unhealthy,
		DesiredIfaceDown: true,
		Action: fmt.Sprintf("recovery-hold — %d/%d stable checks (score %d/%d), demotion %d, vip_iface down",
			streak, confirm, rawScore, maxScore, demotion.Unhealthy),
	}, streak, true
}

func ParsePolicyMode(mode string) PolicyMode {
	switch mode {
	case "preempt":
		return PolicyPreempt
	default:
		return PolicySticky
	}
}
