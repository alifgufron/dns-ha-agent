package agent

type State int

const (
	StateHealthy   State = iota // score >= 80
	StateDegraded               // score >= 40
	StateUnhealthy              // score < 40
)

func (s State) String() string {
	switch s {
	case StateHealthy:
		return "HEALTHY"
	case StateDegraded:
		return "DEGRADED"
	default:
		return "UNHEALTHY"
	}
}

func (s State) Transitioned(from State) bool {
	return s != from
}

func StateFromScore(score int) State {
	if score >= 80 {
		return StateHealthy
	}
	if score >= 40 {
		return StateDegraded
	}
	return StateUnhealthy
}
