package peer

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/health"
)

// These tests exercise the REAL HTTP path (client.Do → classifyHTTPError →
// Diagnose), guarding against regressions where http.Client.Timeout rewrites a
// dial timeout into a generic error that collapses a dead host into a "hang".

func TestCheckPeerHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"score":      100,
			"carp_state": "MASTER",
			"advskew":    0,
			"demotion":   0,
			"preempt":    1,
		})
	}))
	defer srv.Close()

	ph := CheckPeer("127.0.0.1", "node-b", "secret", CheckOptions{
		Port:    portOf(srv.Listener.Addr()),
		Timeout: time.Second,
	})

	if !ph.OK {
		t.Fatalf("expected OK, got error=%q", ph.Error)
	}
	if ph.Score != 100 || ph.State != "HEALTHY" || ph.Carp != "MASTER" {
		t.Errorf("unexpected health: score=%d state=%q carp=%q", ph.Score, ph.State, ph.Carp)
	}
}

func TestCheckPeerRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // now nothing listens → ECONNREFUSED

	ph := CheckPeer("127.0.0.1", "node-b", "secret", CheckOptions{
		Port:    portOfAddr(addr),
		Timeout: time.Second,
	})

	if ph.OK {
		t.Fatal("expected failure, got OK")
	}
	if ph.AgentProbe != ProbeRefused && ph.AgentProbe != ProbeUnreachable {
		t.Errorf("AgentProbe = %v, want Refused or Unreachable", ph.AgentProbe)
	}
}

func TestCheckPeerHang(t *testing.T) {
	// Accept the connection and never write a byte: a hung userland.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			_ = conn // hold open, never respond
		}
	}()

	ph := CheckPeer("127.0.0.1", "node-b", "secret", CheckOptions{
		Port:    portOf(ln.Addr()),
		Timeout: 300 * time.Millisecond,
	})

	if ph.OK {
		t.Fatal("expected failure, got OK")
	}
	if ph.AgentProbe != ProbeNoAnswer {
		t.Errorf("AgentProbe = %v, want NoAnswer (hang)", ph.AgentProbe)
	}
	if ph.Severity != SeverityCritical {
		t.Errorf("Severity = %v, want CRITICAL", ph.Severity)
	}
	if !strings.Contains(ph.Diagnosis, "HUNG") {
		t.Errorf("diagnosis should mention the hang, got %q", ph.Diagnosis)
	}
}

func portOf(addr net.Addr) string {
	return portOfAddr(addr.String())
}

func portOfAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(fmt.Sprintf("bad addr %q: %v", addr, err))
	}
	if _, err := strconv.Atoi(port); err != nil {
		panic(fmt.Sprintf("bad port %q: %v", port, err))
	}
	return port
}

func TestHeartbeatServerAuth(t *testing.T) {
	srv := NewHeartbeatServer("my-secret", []string{"pair-secret"}, "vtnet1", 1, func() health.HealthResult {
		return health.HealthResult{Score: 100}
	}, nil)

	handler := srv.Handler()

	tests := []struct {
		name       string
		headerKey  string
		headerVal  string
		wantStatus int
	}{
		{"valid X-DNS-HA-TOKEN", "X-DNS-HA-TOKEN", "my-secret", http.StatusOK},
		{"valid pairwise X-DNS-HA-TOKEN", "X-DNS-HA-TOKEN", "pair-secret", http.StatusOK},
		{"valid Bearer token", "Authorization", "Bearer my-secret", http.StatusOK},
		{"invalid token", "X-DNS-HA-TOKEN", "wrong-secret", http.StatusUnauthorized},
		{"missing token", "", "", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/health", nil)
			if tc.headerKey != "" {
				req.Header.Set(tc.headerKey, tc.headerVal)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}

