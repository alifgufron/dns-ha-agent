package peer

import (
	"encoding/json"
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
