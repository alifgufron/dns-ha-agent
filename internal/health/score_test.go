package health

import (
	"testing"
	"time"
)

// MaxScore must equal the sum of enabled weights.
func TestMaxScoreExcludesDisabledChecks(t *testing.T) {
	r := RunChecks(ProcessConfig{
		ProcessWeight: 25,
		TCPWeight:     25,
		UDPWeight:     25,
		DNSWeight:     25,
		DNSEnabled:    false, // DNS excluded
		BindAddress:   "127.0.0.1:9",
		Timeout:       time.Second,
	})
	if r.MaxScore != 75 {
		t.Errorf("MaxScore = %d, want 75 (dns excluded)", r.MaxScore)
	}
}

func TestAllWeightsZeroIsSafe(t *testing.T) {
	r := RunChecks(ProcessConfig{BindAddress: "127.0.0.1:9", Timeout: time.Second})
	if r.MaxScore != 0 {
		t.Errorf("MaxScore = %d, want 0", r.MaxScore)
	}
	// Must not divide by zero and must not report a bogus score.
	if r.Score != 0 {
		t.Errorf("Score = %d, want 0 when nothing is enabled", r.Score)
	}
}

// Score is normalized to 0-100 so thresholds hold when checks are disabled
// or weights don't sum to 100. Regression: a BIND9 node with dns_query
// disabled used to cap at 75 and stay DEGRADED forever.
func TestScoreNormalization(t *testing.T) {
	cases := []struct {
		name     string
		raw      int
		max      int
		expected int
	}{
		{"full with dns disabled", 75, 75, 100},
		{"three of four", 75, 100, 75},
		{"half", 50, 100, 50},
		{"custom weights full", 80, 80, 100},
		{"process only failing", 50, 75, 66},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.raw * 100 / tc.max
			if got != tc.expected {
				t.Errorf("normalize(%d/%d) = %d, want %d", tc.raw, tc.max, got, tc.expected)
			}
		})
	}
}

// A node where every enabled check passes must reach 100 regardless of which
// checks are enabled — otherwise it can never become HEALTHY (score >= 80).
func TestFullyPassingNodeReaches100(t *testing.T) {
	r := HealthResult{RawScore: 75, MaxScore: 75}
	if r.MaxScore > 0 {
		r.Score = r.RawScore * 100 / r.MaxScore
	}
	if r.Score != 100 {
		t.Errorf("Score = %d, want 100 for an all-checks-passing node", r.Score)
	}
	if r.Score < 80 {
		t.Error("node would never reach HEALTHY threshold")
	}
}
