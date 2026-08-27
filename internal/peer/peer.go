package peer

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/health"
	"github.com/alifgufron/dns-ha-agent/internal/util"
)

// CheckOptions carries everything a peer check needs beyond the peer identity.
type CheckOptions struct {
	Port      string // agent heartbeat port, without a leading colon
	Timeout   time.Duration
	Ping      bool
	TLS       bool
	DNSPort   string // peer DNS port for the fallback probe
	DNSDomain string // domain used by the fallback UDP query
}

func (o CheckOptions) dnsPort() string {
	if o.DNSPort == "" {
		return "53"
	}
	return o.DNSPort
}

func peerStateFromScore(score int) string {
	if score >= 80 {
		return "HEALTHY"
	}
	if score >= 40 {
		return "DEGRADED"
	}
	return "UNHEALTHY"
}

type PeerEntry struct {
	IP    string
	Name  string
	Token string
}

type PeerHealth struct {
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Score    int       `json:"score"`
	State    string    `json:"state"`
	Carp     string    `json:"carp_state"`
	Advskew  int       `json:"advskew"`
	Demotion int       `json:"demotion"`
	Preempt  int       `json:"preempt"` // peer's net.inet.carp.preempt (1 = kernel reclaims by itself)
	PingOK   bool      `json:"ping_ok"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Updated  time.Time `json:"updated"`

	// Fallback probes, filled only when the heartbeat fails. They answer the
	// question the heartbeat cannot: is the peer still serving DNS, and is it
	// dead or merely hung?
	PingDetail string   `json:"ping_detail,omitempty"` // ICMP RTT on success, ping error otherwise
	AgentProbe Probe    `json:"agent_probe"`
	TCP53      Probe    `json:"tcp53"`
	UDP53OK    bool     `json:"udp53_ok"`
	Severity   Severity `json:"severity"`
	Diagnosis  string   `json:"diagnosis,omitempty"`
}

func CheckPeer(ip, name, token string, opts CheckOptions) PeerHealth {
	ph := PeerHealth{
		Name:    name,
		IP:      ip,
		Updated: time.Now(),
	}

	timeout := opts.Timeout

	scheme := "http"
	var transport *http.Transport
	if opts.TLS {
		scheme = "https"
		// The shared token is the authenticator, not the certificate.
		transport = newDialTransport(&tls.Config{InsecureSkipVerify: true}, timeout)
	} else {
		transport = newDialTransport(nil, timeout)
	}
	defer transport.CloseIdleConnections()

	// No Client.Timeout on purpose: it rewrites dial timeouts into a generic
	// "Client.Timeout exceeded" error that hides whether the host was reachable.
	// The dial/header deadlines live on the transport so classifyHTTPError can
	// tell a dead host (dial timeout) from a hung agent (header timeout).
	client := &http.Client{Transport: transport}

	u := fmt.Sprintf("%s://%s/health", scheme, ip)
	if opts.Port != "" {
		u = fmt.Sprintf("%s://%s:%s/health", scheme, ip, opts.Port)
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		ph.Error = fmt.Sprintf("create request: %v", err)
		return ph
	}
	req.Header.Set("X-DNS-HA-TOKEN", token)

	resp, err := client.Do(req)
	if err != nil {
		// Heartbeat failed. Probe the DNS service directly: an unreachable
		// agent says nothing about whether clients are still being served,
		// and that distinction decides whether this needs waking someone up.
		//
		// ICMP is always probed here, regardless of opts.Ping. That flag only
		// historically gated this call; the reply is cheap and the evidence is
		// needed to tell a dead VM from a hung one.
		ph.AgentProbe = classifyHTTPError(err)
		ph.PingOK, ph.PingDetail = util.PingHost(ip, timeout)
		dnsAddr := net.JoinHostPort(ip, opts.dnsPort())
		ph.TCP53 = probeTCP(dnsAddr, timeout)
		ph.UDP53OK = health.CheckUDP(dnsAddr, opts.DNSDomain, timeout)
		ph.Severity, ph.Diagnosis = Diagnose(ph.AgentProbe, ph.TCP53, ph.UDP53OK, &ph.PingOK)
		ph.Error = fmt.Sprintf("%s: %v", ph.Diagnosis, err)
		return ph
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		ph.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
		return ph
	}

	var peerResp struct {
		Score     int    `json:"score"`
		CarpState string `json:"carp_state"`
		Advskew   int    `json:"advskew"`
		Demotion  int    `json:"demotion"`
		Preempt   int    `json:"preempt"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&peerResp); err != nil {
		ph.Error = fmt.Sprintf("decode response: %v", err)
		return ph
	}

	ph.Score = peerResp.Score
	ph.Carp = peerResp.CarpState
	ph.Advskew = peerResp.Advskew
	ph.Demotion = peerResp.Demotion
	ph.Preempt = peerResp.Preempt
	ph.State = peerStateFromScore(peerResp.Score)
	ph.PingOK = true
	ph.OK = true

	return ph
}

func CheckAllPeers(peers []PeerEntry, opts CheckOptions) []PeerHealth {
	results := make([]PeerHealth, 0, len(peers))
	for _, p := range peers {
		results = append(results, CheckPeer(p.IP, p.Name, p.Token, opts))
	}
	return results
}
