package notify

import (
	"strings"
	"testing"
)

func TestRenderNotification(t *testing.T) {
	subj, body := RenderNotification("UNHEALTHY", "HEALTHY", 25, 255, "10.0.0.1", "10.0.0.10", "BACKUP", "vtnet0", "vtnet1", false, false, false, false)
	if !strings.Contains(subj, "HEALTHY → UNHEALTHY") {
		t.Errorf("subject missing transition: %q", subj)
	}
	if !strings.Contains(body, "State:     UNHEALTHY") {
		t.Errorf("body missing state: %s", body)
	}
	if !strings.Contains(body, "DNS process not running") {
		t.Errorf("body missing process failure reason: %s", body)
	}
}

func TestRenderPeerNotificationDown(t *testing.T) {
	subj, body := RenderPeerNotification("DOWN (unreachable)", "node-b", "10.0.0.2", "peer UNREACHABLE — no ICMP reply", 100, "HEALTHY", "MASTER", "10.0.0.1", PeerProbeInfo{})
	if !strings.Contains(subj, "node-b") || !strings.Contains(subj, "DOWN") {
		t.Errorf("subject wrong: %q", subj)
	}
	if !strings.Contains(body, "no ICMP reply") {
		t.Errorf("body missing error detail: %s", body)
	}
	if !strings.Contains(body, "Status:       DOWN (unreachable)") {
		t.Errorf("body missing status: %s", body)
	}
}

func TestRenderPeerNotificationCritical(t *testing.T) {
	subj, body := RenderPeerNotification(
		"CRITICAL (host up, DNS not serving)", "node-b", "10.0.0.2",
		"DNS is not answering: Get ...: context deadline exceeded",
		100, "HEALTHY", "BACKUP", "10.0.0.1",
		PeerProbeInfo{
			Diagnosis:  "peer userland HUNG — the agent accepted the connection but never replied",
			LastCarp:   "MASTER",
			Ping:       "✓ reply, 0.458 ms",
			AgentProbe: "⚠ connected but no reply (hung)",
			TCP53:      "✗ connection refused (nothing listening)",
			UDP53:      "✗ not answering DNS queries",
		},
	)
	if !strings.Contains(subj, "CRITICAL") {
		t.Errorf("subject wrong: %q", subj)
	}
	if !strings.Contains(body, "Diagnosis:    peer userland HUNG") {
		t.Errorf("body missing diagnosis: %s", body)
	}
	if !strings.Contains(body, "Last CARP:    MASTER") {
		t.Errorf("body missing last carp: %s", body)
	}
	if !strings.Contains(body, "ICMP:        ✓ reply, 0.458 ms") {
		t.Errorf("body missing ping probe: %s", body)
	}
	if !strings.Contains(body, "Agent HTTP:  ⚠ connected but no reply (hung)") {
		t.Errorf("body missing agent probe: %s", body)
	}
	if !strings.Contains(body, "UDP :53:     ✗ not answering DNS queries") {
		t.Errorf("body missing udp probe: %s", body)
	}
}

func TestRenderPeerNotificationUp(t *testing.T) {
	subj, _ := RenderPeerNotification("UP (recovered)", "node-b", "10.0.0.2", "", 100, "HEALTHY", "MASTER", "10.0.0.1", PeerProbeInfo{})
	if !strings.Contains(subj, "UP") {
		t.Errorf("subject wrong: %q", subj)
	}
}

func TestRenderMasterLoss(t *testing.T) {
	subj, body := RenderMasterLoss("lost without higher-priority peer", 100, "BACKUP", "10.0.0.1")
	if !strings.Contains(subj, "unexpected VIP loss") {
		t.Errorf("subject wrong: %q", subj)
	}
	if !strings.Contains(body, "split") && !strings.Contains(body, "Possible causes") {
		t.Errorf("body missing guidance: %s", body)
	}
}
