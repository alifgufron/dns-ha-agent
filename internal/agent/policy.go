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

func ParsePolicyMode(mode string) PolicyMode {
	switch mode {
	case "preempt":
		return PolicyPreempt
	default:
		return PolicySticky
	}
}
