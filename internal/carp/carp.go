package carp

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

type State int

const (
	StateUnknown State = iota
	StateMaster
	StateBackup
)

func (s State) String() string {
	switch s {
	case StateMaster:
		return "MASTER"
	case StateBackup:
		return "BACKUP"
	default:
		return "UNKNOWN"
	}
}

// carpStateVhidRe matches the CARP line including its vhid, e.g.
// "carp: MASTER vhid 1 advbase 1 advskew 0" — required so a multi-VHID
// interface reports the state of the configured VHID, not the first one listed.
var carpStateVhidRe = regexp.MustCompile(`carp:\s+(MASTER|BACKUP|INIT)\s+vhid\s+(\d+)`)
var carpStateRe = regexp.MustCompile(`carp:\s+(MASTER|BACKUP)`)
var vhidVIP4Re = regexp.MustCompile(`inet\s+(\S+)\s+.*vhid\s+(\d+)`)
var vhidVIP6Re = regexp.MustCompile(`inet6\s+(\S+)\s+.*vhid\s+(\d+)`)
var inetRe = regexp.MustCompile(`inet\s+(\S+)`)
var advskewRe = regexp.MustCompile(`carp:.*vhid\s+(\d+).*advskew\s+(\d+)`)

func ReadState(iface string, vhid int) (State, error) {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface)
	if result.Err != nil {
		return StateUnknown, fmt.Errorf("ifconfig %s: %w (stderr: %s)", iface, result.Err, result.Stderr)
	}

	if !carpStateRe.MatchString(result.Stdout) && !carpStateVhidRe.MatchString(result.Stdout) {
		return StateUnknown, fmt.Errorf("no carp state found for vhid %d on interface %s", vhid, iface)
	}
	return parseCarpState(result.Stdout, vhid), nil
}

// parseCarpState picks the CARP line for the given VHID (an interface may carry
// several), falling back to the first carp line when no vhid is present.
// INIT and unknown states map to StateUnknown.
func parseCarpState(out string, vhid int) State {
	vhidStr := strconv.Itoa(vhid)
	state := ""
	for _, m := range carpStateVhidRe.FindAllStringSubmatch(out, -1) {
		if m[2] == vhidStr {
			state = m[1]
			break
		}
	}
	if state == "" {
		if m := carpStateRe.FindStringSubmatch(out); len(m) >= 2 {
			state = m[1]
		}
	}

	switch state {
	case "MASTER":
		return StateMaster
	case "BACKUP":
		return StateBackup
	default:
		return StateUnknown
	}
}

func GetNodeIP(iface string) (string, error) {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface)
	if result.Err != nil {
		return "", fmt.Errorf("ifconfig %s: %w", iface, result.Err)
	}

	lines := strings.Split(result.Stdout, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "vhid") {
			matches := inetRe.FindStringSubmatch(line)
			if matches != nil {
				ip := net.ParseIP(matches[1])
				if ip != nil {
					return ip.String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("no non-VIP IP found on %s", iface)
}

func GetVIP(iface string, vhid int) (string, error) {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface)
	if result.Err != nil {
		return "", fmt.Errorf("ifconfig %s: %w", iface, result.Err)
	}

	vips := parseVIPs(result.Stdout, vhid)
	if len(vips) == 0 {
		return "", fmt.Errorf("no VIP found for vhid %d on %s", vhid, iface)
	}

	return strings.Join(vips, ", "), nil
}

// parseVIPs returns all VIPs (IPv4 and IPv6) belonging to the given VHID.
func parseVIPs(out string, vhid int) []string {
	vhidStr := strconv.Itoa(vhid)
	var vips []string

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		for _, re := range []*regexp.Regexp{vhidVIP4Re, vhidVIP6Re} {
			m := re.FindStringSubmatch(line)
			if m != nil && m[2] == vhidStr {
				if ip := net.ParseIP(m[1]); ip != nil {
					vips = append(vips, ip.String())
				}
			}
		}
	}
	return vips
}

func GetAdvskew(iface string, vhid int) (int, error) {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface)
	if result.Err != nil {
		return 0, fmt.Errorf("ifconfig %s: %w", iface, result.Err)
	}

	if skew, ok := parseAdvskew(result.Stdout, vhid); ok {
		return skew, nil
	}

	return 0, fmt.Errorf("no advskew found for vhid %d on %s", vhid, iface)
}

// parseAdvskew returns the configured advskew for the given VHID.
func parseAdvskew(out string, vhid int) (int, bool) {
	vhidStr := strconv.Itoa(vhid)
	for _, line := range strings.Split(out, "\n") {
		m := advskewRe.FindStringSubmatch(line)
		if m != nil && m[1] == vhidStr {
			skew, err := strconv.Atoi(m[2])
			if err != nil {
				return 0, false
			}
			return skew, true
		}
	}
	return 0, false
}
