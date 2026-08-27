package peer

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
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

// dialError tags a failed TCP handshake inside an http.Client error chain.
//
// http.Client.Timeout discards the *net.OpError that would tell a dial failure
// (host down, no route) apart from a post-connect hang, collapsing both into
// the same "context deadline exceeded". Wrapping dial errors in this sentinel
// at the transport layer keeps that distinction: anything NOT tagged happened
// after the socket was accepted, so the peer is up but hung.
type dialError struct{ err error }

func (e *dialError) Error() string { return e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

// newDialTransport returns an http.Transport whose dial step tags handshake
// failures with dialError so classifyHTTPError can separate them from timeouts
// that happen after the connection was accepted.
//
// The dial and header timeouts live on the transport (Dialer.Timeout +
// ResponseHeaderTimeout), NOT on http.Client.Timeout. http.Client.Timeout —
// like a request context deadline — replaces a dial timeout with its own
// "Client.Timeout exceeded" error, discarding the dialError sentinel and
// collapsing a dead host into a "hang". Transport-level timeouts preserve the
// original error all the way up, so the two stay distinguishable.
func newDialTransport(tlsConf *tls.Config, timeout time.Duration) *http.Transport {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := &http.Transport{TLSClientConfig: tlsConf}
	dialer := &net.Dialer{Timeout: timeout}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, &dialError{err}
		}
		return conn, nil
	}
	transport.ResponseHeaderTimeout = timeout
	return transport
}

// classifyHTTPError maps a net/http client error onto a Probe.
//
// A dialError means the TCP handshake never completed — host down, filtered,
// or refused. Anything else means the socket WAS accepted and the peer never
// answered, which is what a hung userland looks like from the outside.
func classifyHTTPError(err error) Probe {
	if err == nil {
		return ProbeOK
	}
	var de *dialError
	if errors.As(err, &de) {
		if errors.Is(de.err, syscall.ECONNREFUSED) {
			return ProbeRefused
		}
		return ProbeUnreachable
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ProbeRefused
	}
	return ProbeNoAnswer
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
//
// pingOK, when non-nil, is ICMP evidence the host's kernel is alive. It is
// weaker than an answered probe (a firewall may drop ICMP on a healthy host)
// so it only contributes to "host is up", never to "host is down".
func Diagnose(agent, tcp53 Probe, udp53OK bool, pingOK *bool) (Severity, string) {
	// A refused connection proves the kernel is alive: only a running network
	// stack replies with RST. So "nothing answered at all" is what marks a host
	// as gone. This is firmer evidence than an ICMP reply, which a firewall may
	// drop on a perfectly healthy host.
	hostUp := udp53OK ||
		agent == ProbeOK || agent == ProbeRefused || agent == ProbeNoAnswer ||
		tcp53 == ProbeOK || tcp53 == ProbeRefused ||
		(pingOK != nil && *pingOK)
	if !hostUp {
		return SeverityHostDown, "host unreachable — no ICMP reply and nothing answered on any port (VM down or network partition)"
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
	case pingOK != nil && *pingOK:
		return SeverityCritical, "host responds to ICMP but DNS is not answering (service down or port filtered)"
	default:
		return SeverityCritical, "DNS is not answering"
	}
}
