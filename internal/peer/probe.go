package peer

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

// Probe is how a TCP service answered a reachability check.
//
// The split between "refused" and "connected but silent" is the whole point:
// a dead process cannot complete a TCP handshake — the kernel answers with RST
// — while a hung one leaves the listening socket open and simply never replies.
// Collapsing both into "unreachable" throws away the only external evidence
// that separates a crashed node from a frozen one.
type Probe int

const (
	ProbeUnknown     Probe = iota
	ProbeOK                // handshake completed and the service answered
	ProbeRefused           // RST — nothing is listening, the process is gone
	ProbeNoAnswer          // handshake completed, no reply — the process is hung
	ProbeUnreachable       // no handshake at all — host down, filtered, or RTO
)

func (p Probe) String() string {
	switch p {
	case ProbeOK:
		return "✓ responding"
	case ProbeRefused:
		return "✗ connection refused (nothing listening)"
	case ProbeNoAnswer:
		return "⚠ connected but no reply (hung)"
	case ProbeUnreachable:
		return "✗ unreachable (timeout / no route)"
	default:
		return "? not probed"
	}
}

// classifyHTTPError maps a net/http client error onto a Probe.
//
// A failure during "dial" means the TCP handshake never completed. Any failure
// after that means the socket WAS accepted and the peer never answered — which
// is what a hung userland looks like from the outside.
func classifyHTTPError(err error) Probe {
	if err == nil {
		return ProbeOK
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ProbeRefused
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return ProbeUnreachable
	}
	if os.IsTimeout(err) {
		return ProbeNoAnswer
	}
	return ProbeUnreachable
}

// probeTCP reports whether a TCP port accepts connections. Only the handshake
// is tested, so a refused port (process gone) is distinguishable from a port
// that never answers (host gone).
func probeTCP(address string, timeout time.Duration) Probe {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err == nil {
		conn.Close()
		return ProbeOK
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ProbeRefused
	}
	return ProbeUnreachable
}

// Severity ranks what a failed heartbeat means for actual service delivery.
// An unreachable agent is not by itself an outage; an unanswered DNS query is.
type Severity int

const (
	SeverityNone      Severity = iota
	SeverityHostDown           // no response of any kind — VM down or network cut
	SeverityAgentOnly          // agent unreachable, DNS still answering
	SeverityCritical           // DNS not answering
)

func (s Severity) String() string {
	switch s {
	case SeverityHostDown:
		return "DOWN (host unreachable)"
	case SeverityAgentOnly:
		return "DEGRADED (agent down, DNS serving)"
	case SeverityCritical:
		return "CRITICAL (host up, DNS not serving)"
	default:
		return "OK"
	}
}

// Diagnose turns raw probe results into an operator-facing conclusion.
//
// DNS delivery is judged by the UDP query alone. That is the transport clients
// actually use, and it is the only probe that proves the server still answers
// rather than merely holding a socket open — a hung BIND9 still accepts TCP.
func Diagnose(agent, tcp53 Probe, udp53OK bool) (Severity, string) {
	// A refused connection proves the kernel is alive: only a running network
	// stack replies with RST. So "nothing answered at all" is what marks a host
	// as gone. This is firmer evidence than an ICMP reply, which a firewall may
	// drop on a perfectly healthy host.
	hostUp := udp53OK ||
		agent == ProbeOK || agent == ProbeRefused || agent == ProbeNoAnswer ||
		tcp53 == ProbeOK || tcp53 == ProbeRefused
	if !hostUp {
		return SeverityHostDown, "host unreachable — nothing answered on any port (VM down or network partition)"
	}

	if udp53OK {
		if agent == ProbeNoAnswer {
			return SeverityAgentOnly, "agent hung (connection accepted, no reply) — DNS is still answering"
		}
		return SeverityAgentOnly, "agent not reachable — DNS is still answering"
	}

	switch {
	case agent == ProbeNoAnswer:
		return SeverityCritical, "peer userland HUNG — the agent accepted the connection but never replied, and DNS is not answering"
	case agent == ProbeRefused && tcp53 == ProbeRefused:
		return SeverityCritical, "agent and DNS processes are both down"
	default:
		return SeverityCritical, "DNS is not answering"
	}
}
