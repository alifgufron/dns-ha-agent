package peer

import (
	"context"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"
)

func dialErr(err error) error { return &dialError{err} }

// classifyHTTPError must tell "nothing is listening" (refused) apart from
// "accepted but never answered" (a hung userland). Collapsing the two is what
// made a hang look like an ordinary unreachable peer. Dial failures arrive
// wrapped in dialError by the custom transport — mirroring the real
// http.Client chain (url.Error → dialError) for both the dead-host and the
// hung-host paths.
func TestClassifyHTTPError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Probe
	}{
		{"nil", nil, ProbeOK},
		{"refused", dialErr(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), ProbeRefused},
		{"dial timeout", dialErr(&net.OpError{Op: "dial", Err: context.DeadlineExceeded}), ProbeUnreachable},
		{"dial host unreachable", dialErr(&net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}), ProbeUnreachable},
		{"url dial timeout — host down", &url.Error{Op: "Get", URL: "http://x", Err: dialErr(&net.OpError{Op: "dial", Err: context.DeadlineExceeded})}, ProbeUnreachable},
		{"url refused", &url.Error{Op: "Get", URL: "http://x", Err: dialErr(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED})}, ProbeRefused},
		{"timeout after connect", &net.OpError{Op: "read", Err: context.DeadlineExceeded}, ProbeNoAnswer},
		{"url timeout after connect — hang", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "read", Err: context.DeadlineExceeded}}, ProbeNoAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyHTTPError(c.err); got != c.want {
				t.Errorf("classifyHTTPError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// Diagnose must rank a host that answers NOTHING as down, and a host whose DNS
// is silent while the kernel answers as critical — the exact hang scenario.
func TestDiagnose(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	cases := []struct {
		name     string
		agent    Probe
		tcp53    Probe
		udp53    bool
		ping     *bool
		wantSev  Severity
		wantHang bool
	}{
		{"host down — nothing answers", ProbeUnreachable, ProbeUnreachable, false, boolPtr(false), SeverityHostDown, false},
		{"host down but ICMP replies — critical", ProbeUnreachable, ProbeUnreachable, false, boolPtr(true), SeverityCritical, false},
		{"agent down, DNS serving", ProbeRefused, ProbeOK, true, nil, SeverityAgentOnly, false},
		{"agent hung, DNS serving", ProbeNoAnswer, ProbeOK, true, nil, SeverityAgentOnly, false},
		{"agent hung, DNS dead", ProbeNoAnswer, ProbeRefused, false, nil, SeverityCritical, true},
		{"agent and DNS both dead", ProbeRefused, ProbeRefused, false, nil, SeverityCritical, false},
		{"agent dead, DNS tcp only", ProbeRefused, ProbeOK, false, nil, SeverityCritical, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sev, diag := Diagnose(c.agent, c.tcp53, c.udp53, c.ping)
			if sev != c.wantSev {
				t.Errorf("Diagnose severity = %v, want %v", sev, c.wantSev)
			}
			if c.wantHang && !contains(diag, "HUNG") {
				t.Errorf("expected a hang diagnosis, got %q", diag)
			}
		})
	}
}

// A TCP handshake that fails with ECONNREFUSED must stay refused, and a
// successful dial must stay OK — probeTCP is what the DNS probe rests on.
func TestProbeTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if got := probeTCP(ln.Addr().String(), time.Second); got != ProbeOK {
		t.Errorf("listening socket = %v, want OK", got)
	}
	if got := probeTCP("127.0.0.1:1", time.Second); got != ProbeRefused {
		t.Errorf("closed port = %v, want Refused", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
