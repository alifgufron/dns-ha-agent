package peer

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/health"
)

type HeartbeatServer struct {
	tokens   map[string]bool // global token + per-peer tokens (pairwise auth)
	vipIface string
	vhid     int
	healthFn func() health.HealthResult
	mu       sync.RWMutex
	log      *slog.Logger
}

func NewHeartbeatServer(token string, peerTokens []string, vipIface string, vhid int, healthFn func() health.HealthResult, log *slog.Logger) *HeartbeatServer {
	tokens := make(map[string]bool)
	if token != "" {
		tokens[token] = true
	}
	for _, t := range peerTokens {
		if t != "" {
			tokens[t] = true
		}
	}
	return &HeartbeatServer{
		tokens:   tokens,
		vipIface: vipIface,
		vhid:     vhid,
		healthFn: healthFn,
		log:      log,
	}
}

func (s *HeartbeatServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	return s.authMiddleware(mux)
}

func (s *HeartbeatServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-DNS-HA-TOKEN")
		if token == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		s.mu.RLock()
		ok := s.tokens[token]
		s.mu.RUnlock()
		if !ok {
			s.log.Warn("unauthorized peer request", "remote", r.RemoteAddr, "path", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *HeartbeatServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := s.healthFn()

	cs, err := carp.ReadState(s.vipIface, s.vhid)
	carpState := "UNKNOWN"
	if err == nil {
		carpState = cs.String()
	}

	advskew, _ := carp.GetAdvskew(s.vipIface, s.vhid)
	demotion, _ := carp.GetDemotion()
	preempt, _ := carp.GetPreempt()

	resp := struct {
		Score     int    `json:"score"`
		CarpState string `json:"carp_state"`
		Advskew   int    `json:"advskew"`
		Demotion  int    `json:"demotion"`
		Preempt   int    `json:"preempt"`
		Timestamp string `json:"timestamp"`
	}{
		Score:     h.Score,
		CarpState: carpState,
		Advskew:   advskew,
		Demotion:  demotion,
		Preempt:   preempt,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *HeartbeatServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	h := s.healthFn()

	cs, err := carp.ReadState(s.vipIface, s.vhid)
	carpStateStr := "UNKNOWN"
	carpMasterVal := 0
	carpBackupVal := 0
	if err == nil {
		carpStateStr = cs.String()
		if cs == carp.StateMaster {
			carpMasterVal = 1
		} else if cs == carp.StateBackup {
			carpBackupVal = 1
		}
	}

	advskew, _ := carp.GetAdvskew(s.vipIface, s.vhid)
	demotion, _ := carp.GetDemotion()
	preempt, _ := carp.GetPreempt()

	bToI := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var sb strings.Builder
	sb.WriteString("# HELP dns_ha_health_score Current health score of local DNS node (0-100)\n")
	sb.WriteString("# TYPE dns_ha_health_score gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_health_score %d\n\n", h.Score))

	sb.WriteString("# HELP dns_ha_carp_role CARP role of node (1=Active role, 0=Inactive)\n")
	sb.WriteString("# TYPE dns_ha_carp_role gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_carp_role{role=\"MASTER\",interface=\"%s\",vhid=\"%d\"} %d\n", s.vipIface, s.vhid, carpMasterVal))
	sb.WriteString(fmt.Sprintf("dns_ha_carp_role{role=\"BACKUP\",interface=\"%s\",vhid=\"%d\"} %d\n\n", s.vipIface, s.vhid, carpBackupVal))

	sb.WriteString("# HELP dns_ha_advskew CARP advskew configured on VIP interface\n")
	sb.WriteString("# TYPE dns_ha_advskew gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_advskew{interface=\"%s\",vhid=\"%d\"} %d\n\n", s.vipIface, s.vhid, advskew))

	sb.WriteString("# HELP dns_ha_demotion_factor Current CARP demotion counter\n")
	sb.WriteString("# TYPE dns_ha_demotion_factor gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_demotion_factor %d\n\n", demotion))

	sb.WriteString("# HELP dns_ha_kernel_preempt FreeBSD net.inet.carp.preempt sysctl status\n")
	sb.WriteString("# TYPE dns_ha_kernel_preempt gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_kernel_preempt %d\n\n", preempt))

	sb.WriteString("# HELP dns_ha_check_status Health check pass status (1=OK, 0=FAIL)\n")
	sb.WriteString("# TYPE dns_ha_check_status gauge\n")
	sb.WriteString(fmt.Sprintf("dns_ha_check_status{check=\"process\"} %d\n", bToI(h.ProcessAlive)))
	sb.WriteString(fmt.Sprintf("dns_ha_check_status{check=\"tcp\"} %d\n", bToI(h.TCPAlive)))
	sb.WriteString(fmt.Sprintf("dns_ha_check_status{check=\"udp\"} %d\n", bToI(h.UDPAlive)))
	sb.WriteString(fmt.Sprintf("dns_ha_check_status{check=\"dns\"} %d\n\n", bToI(h.DNSAlive)))

	if h.DNSDetail.Success {
		rttSec := h.DNSDetail.RTT.Seconds()
		sb.WriteString("# HELP dns_ha_check_rtt_seconds Latency of DNS health query in seconds\n")
		sb.WriteString("# TYPE dns_ha_check_rtt_seconds gauge\n")
		sb.WriteString(fmt.Sprintf("dns_ha_check_rtt_seconds{check=\"dns\"} %f\n\n", rttSec))
	}

	_ = carpStateStr
	w.Write([]byte(sb.String()))
}
