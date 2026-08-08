package peer

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

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
}

func CheckPeer(ip, name, token, port string, timeout time.Duration, pingEnabled, tlsEnabled bool) PeerHealth {
	ph := PeerHealth{
		Name:    name,
		IP:      ip,
		Updated: time.Now(),
	}

	scheme := "http"
	transport := http.DefaultTransport
	if tlsEnabled {
		scheme = "https"
		// The shared token is the authenticator, not the certificate.
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	client := &http.Client{Timeout: timeout, Transport: transport}

	u := fmt.Sprintf("%s://%s/health", scheme, ip)
	if port != "" {
		u = fmt.Sprintf("%s://%s:%s/health", scheme, ip, port)
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		ph.Error = fmt.Sprintf("create request: %v", err)
		return ph
	}
	req.Header.Set("X-HA-DDIST-TOKEN", token)

	resp, err := client.Do(req)
	if err != nil {
		// Heartbeat failed — distinguish host-down vs service-down via ping
		if pingEnabled {
			ph.PingOK = util.PingHost(ip, timeout)
			if ph.PingOK {
				ph.Error = fmt.Sprintf("peer HTTP unreachable (host UP, agent/service DOWN): %v", err)
			} else {
				ph.Error = fmt.Sprintf("peer UNREACHABLE — no ICMP reply (host DOWN or RTO): %v", err)
			}
		} else {
			ph.Error = fmt.Sprintf("connection failed: %v", err)
		}
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

func CheckAllPeers(peers []PeerEntry, port string, timeout time.Duration, pingEnabled, tlsEnabled bool) []PeerHealth {
	results := make([]PeerHealth, 0, len(peers))
	for _, p := range peers {
		ph := CheckPeer(p.IP, p.Name, p.Token, port, timeout, pingEnabled, tlsEnabled)
		results = append(results, ph)
	}
	return results
}
