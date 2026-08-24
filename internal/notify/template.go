package notify

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

const defaultTemplate = `DNS HA Agent — State Change

Timestamp: {{.Timestamp}}
Hostname:  {{.Hostname}}

── Management Interface ({{.MgmtInterface}}) ──
Node IP:   {{.NodeIP}}

── VIP/CARP Interface ({{.VIPInterface}}) ──
VIP:       {{.VIP}}
CARP:      {{.CarpState}}

State:     {{.NewState}}
Previous:  {{.OldState}}

Checks:
  Process:  {{.ProcessOK}}
  TCP :53:  {{.TCPOK}}
  UDP :53:  {{.UDPOK}}
  DNS:      {{.DNSOK}}
  Score:    {{.Score}}/100
  Demotion: {{.Demotion}}

Reason: {{.Reason}}

Recent CARP/ARP messages from /var/log/messages:
{{.CarpArpLog}}

This is an automated notification from dns-ha-agent.
`

type NotificationData struct {
	Timestamp     string
	Hostname      string
	MgmtInterface string
	VIPInterface  string
	NodeIP        string
	VIP           string
	CarpState     string
	CarpArpLog    string
	NewState      string
	OldState      string
	Score         int
	Demotion      int
	Reason        string
	ProcessOK     string
	TCPOK         string
	UDPOK         string
	DNSOK         string
}

func checkSymbol(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func generateReason(processOK, tcpOK, udpOK, dnsOK bool) string {
	var failed []string
	if !processOK {
		failed = append(failed, "DNS process not running")
	}
	if !tcpOK {
		failed = append(failed, "TCP :53 not responding")
	}
	if !udpOK {
		failed = append(failed, "UDP :53 not responding")
	}
	if !dnsOK {
		failed = append(failed, "DNS query failed")
	}

	if len(failed) == 0 {
		return "all checks passed"
	}
	return "Failed: " + strings.Join(failed, ", ") + "."
}

func getCarpArpLog() string {
	res := util.ExecTimeout(2*time.Second, "sh", "-c", "grep -E 'carp:|arp:' /var/log/messages 2>/dev/null | tail -10")
	if res.Err != nil || len(res.Stdout) == 0 {
		return "  (no recent CARP/ARP messages)"
	}
	return "  " + strings.TrimSpace(strings.ReplaceAll(res.Stdout, "\n", "\n  "))
}

func RenderNotification(newState, oldState string, score, demotion int, nodeIP, vip, carpState, mgmtIface, vipIface string, processOK, tcpOK, udpOK, dnsOK bool) (string, string) {
	hostname, _ := os.Hostname()

	data := NotificationData{
		Timestamp:     time.Now().Format(time.RFC3339),
		Hostname:      hostname,
		MgmtInterface: mgmtIface,
		VIPInterface:  vipIface,
		NodeIP:        nodeIP,
		VIP:           vip,
		CarpState:     carpState,
		CarpArpLog:    getCarpArpLog(),
		NewState:      newState,
		OldState:      oldState,
		Score:         score,
		Demotion:      demotion,
		Reason:        generateReason(processOK, tcpOK, udpOK, dnsOK),
		ProcessOK:     checkSymbol(processOK),
		TCPOK:         checkSymbol(tcpOK),
		UDPOK:         checkSymbol(udpOK),
		DNSOK:         checkSymbol(dnsOK),
	}

	tmpl, err := template.New("notification").Parse(defaultTemplate)
	if err != nil {
		return fmt.Sprintf("DNS HA: %s → %s", oldState, newState),
			fmt.Sprintf("State changed from %s to %s (score: %d)", oldState, newState, score)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("DNS HA: %s → %s", oldState, newState),
			fmt.Sprintf("State changed from %s to %s (score: %d)", oldState, newState, score)
	}

	subject := fmt.Sprintf("DNS HA: %s → %s on %s", oldState, newState, hostname)

	return subject, buf.String()
}

const masterLossTemplate = `DNS HA Agent — Unexpected VIP Loss

Timestamp: {{.Timestamp}}
Hostname:  {{.Hostname}}

WARNING: This node lost the CARP MASTER state (the VIP) while HEALTHY, but no
healthy peer with higher priority (lower effective advskew) was detected to
justify the step-down. Possible causes:

  - Another node/VM claimed the VIP without being configured as a peer
  - CARP state desync / interface flap
  - A peer agent is not running (heartbeat missing)

── This Node ──
Node IP:   {{.NodeIP}}
State:     {{.LocalState}}
Score:     {{.LocalScore}}/100
CARP:      {{.LocalCarp}}
Reason:    {{.Reason}}

Recent CARP/ARP messages from /var/log/messages:
{{.CarpArpLog}}

This is an automated notification from dns-ha-agent.
`

type MasterLossData struct {
	Timestamp  string
	Hostname   string
	NodeIP     string
	LocalState string
	LocalScore int
	LocalCarp  string
	Reason     string
	CarpArpLog string
}

func RenderMasterLoss(reason string, localScore int, localCarp, nodeIP string) (string, string) {
	hostname, _ := os.Hostname()

	data := MasterLossData{
		Timestamp:  time.Now().Format(time.RFC3339),
		Hostname:   hostname,
		NodeIP:     nodeIP,
		LocalState: "HEALTHY",
		LocalScore: localScore,
		LocalCarp:  localCarp,
		Reason:     reason,
		CarpArpLog: getCarpArpLog(),
	}

	tmpl, err := template.New("masterloss").Parse(masterLossTemplate)
	if err != nil {
		return "DNS HA: unexpected VIP loss",
			fmt.Sprintf("Unexpected VIP loss on %s: %s", hostname, reason)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "DNS HA: unexpected VIP loss",
			fmt.Sprintf("Unexpected VIP loss on %s: %s", hostname, reason)
	}

	subject := fmt.Sprintf("DNS HA: unexpected VIP loss on %s", hostname)

	return subject, buf.String()
}

const peerTemplate = `DNS HA Agent — Peer Status

Timestamp: {{.Timestamp}}
Hostname:  {{.Hostname}}

── This Node ──
Node IP:   {{.NodeIP}}
State:     {{.LocalState}}
Score:     {{.LocalScore}}/100
CARP:      {{.LocalCarp}}

── Peer ({{.PeerName}}) ──
Peer IP:      {{.PeerIP}}
Status:       {{.Status}}
{{if .Diagnosis}}Diagnosis:    {{.Diagnosis}}
{{end}}{{if .LastCarp}}Last CARP:    {{.LastCarp}}
{{end}}{{if .Probes}}Probes:{{.Probes}}
{{end}}{{if .Error}}Error:        {{.Error}}
{{end}}
Recent CARP/ARP messages from /var/log/messages:
{{.CarpArpLog}}

This is an automated notification from dns-ha-agent.
`

// PeerProbeInfo carries the fallback probe results that explain WHY a peer's
// heartbeat failed. Plain strings keep the notify package independent of the
// peer package.
type PeerProbeInfo struct {
	Diagnosis  string // human conclusion: hung, host down, agent down, ...
	LastCarp   string // CARP state the peer last reported while healthy
	Ping       string // ICMP probe result (RTT or error)
	AgentProbe string // agent heartbeat port probe result
	TCP53      string // TCP :53 probe result
	UDP53      string // UDP :53 (DNS query) probe result
}

// formatProbes renders the probe detail block, omitting probes that never ran.
func formatProbes(info PeerProbeInfo) string {
	var b strings.Builder
	write := func(label, v string) {
		if v != "" {
			b.WriteString("\n" + label + v)
		}
	}
	write("Agent HTTP:  ", info.AgentProbe)
	write("ICMP:        ", info.Ping)
	write("TCP :53:     ", info.TCP53)
	write("UDP :53:     ", info.UDP53)
	return b.String()
}

type PeerNotificationData struct {
	Timestamp  string
	Hostname   string
	NodeIP     string
	LocalState string
	LocalScore int
	LocalCarp  string
	PeerName   string
	PeerIP     string
	Status     string
	Error      string
	Diagnosis  string
	LastCarp   string
	Probes     string
	CarpArpLog string
}

func RenderPeerNotification(status, peerName, peerIP, errorMsg string, localScore int, localState, localCarp, nodeIP string, info PeerProbeInfo) (string, string) {
	hostname, _ := os.Hostname()

	data := PeerNotificationData{
		Timestamp:  time.Now().Format(time.RFC3339),
		Hostname:   hostname,
		NodeIP:     nodeIP,
		LocalState: localState,
		LocalScore: localScore,
		LocalCarp:  localCarp,
		PeerName:   peerName,
		PeerIP:     peerIP,
		Status:     status,
		Error:      errorMsg,
		Diagnosis:  info.Diagnosis,
		LastCarp:   info.LastCarp,
		Probes:     formatProbes(info),
		CarpArpLog: getCarpArpLog(),
	}

	tmpl, err := template.New("peer").Parse(peerTemplate)
	if err != nil {
		return fmt.Sprintf("DNS HA: peer %s %s", peerName, status),
			fmt.Sprintf("Peer %s (%s) is %s", peerName, peerIP, status)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("DNS HA: peer %s %s", peerName, status),
			fmt.Sprintf("Peer %s (%s) is %s", peerName, peerIP, status)
	}

	subject := fmt.Sprintf("DNS HA: peer %s %s", peerName, status)

	return subject, buf.String()
}
