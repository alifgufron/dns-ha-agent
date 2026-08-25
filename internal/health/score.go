package health

import (
	"time"
)

type Weights struct {
	Process int
	TCP     int
	UDP     int
	DNS     int
}

var DefaultWeights = Weights{
	Process: 25,
	TCP:     25,
	UDP:     25,
	DNS:     25,
}

type HealthResult struct {
	// Score is normalized to 0-100 (percentage of MaxScore) so the
	// HEALTHY/DEGRADED/UNHEALTHY thresholds hold regardless of which checks
	// are enabled or how weights are configured.
	Score        int
	RawScore     int // sum of passing weights, before normalization
	ProcessAlive bool
	TCPAlive     bool
	UDPAlive     bool
	DNSAlive     bool
	DNSDetail    DNSResult
	MaxScore     int
}

func RunChecks(cfg ProcessConfig) HealthResult {
	// Weights are absolute: 0 = check disabled (no score contribution).
	// Caller (runner) fills defaults per enabled check.
	w := Weights{
		Process: cfg.ProcessWeight,
		TCP:     cfg.TCPWeight,
		UDP:     cfg.UDPWeight,
		DNS:     cfg.DNSWeight,
	}
	if !cfg.DNSEnabled {
		w.DNS = 0
	}

	timeout := 2 * time.Second
	if cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}

	address := "127.0.0.1:53"
	if cfg.BindAddress != "" {
		address = cfg.BindAddress
	}

	result := HealthResult{
		MaxScore: w.Process + w.TCP + w.UDP + w.DNS,
	}

	result.ProcessAlive = CheckProcesses(cfg.ProcessNames, timeout)
	if result.ProcessAlive {
		result.RawScore += w.Process
	}

	result.TCPAlive = CheckTCP(address, timeout)
	if result.TCPAlive {
		result.RawScore += w.TCP
	}

	udpDomain := cfg.DNSDomain
	if udpDomain == "" && len(cfg.DNSDomains) > 0 {
		udpDomain = cfg.DNSDomains[0]
	}
	result.UDPAlive = CheckUDP(address, udpDomain, timeout)
	if result.UDPAlive {
		result.RawScore += w.UDP
	}

	if cfg.DNSEnabled {
		domains := cfg.DNSDomains
		if len(domains) == 0 {
			if cfg.DNSDomain != "" {
				domains = []string{cfg.DNSDomain}
			} else {
				domains = []string{"google.com"}
			}
		}

		allSuccess := true
		anySlow := false
		var totalRTT time.Duration
		var lastDetail DNSResult

		for _, dom := range domains {
			res := CheckDNSQuery(dom, cfg.DNSRecordType, address, timeout)
			if !res.Success {
				allSuccess = false
				lastDetail = res
				break
			}
			totalRTT += res.RTT
			if cfg.DNSLatencyThreshold > 0 && res.RTT > cfg.DNSLatencyThreshold {
				anySlow = true
			}
			lastDetail = res
		}

		if len(domains) > 0 {
			lastDetail.RTT = totalRTT / time.Duration(len(domains))
		}
		lastDetail.Success = allSuccess
		lastDetail.Slow = anySlow
		result.DNSDetail = lastDetail
		result.DNSAlive = allSuccess

		if result.DNSAlive {
			if anySlow {
				// SLA Penalty: query succeeded but exceeded latency threshold, award 50% DNS weight
				result.RawScore += w.DNS / 2
			} else {
				result.RawScore += w.DNS
			}
		}
	}

	// Normalize to 0-100. Without this, disabling a check (or using weights
	// that don't sum to 100) would cap a fully healthy node below the
	// HEALTHY threshold — e.g. dns_query disabled → max 75 → always DEGRADED.
	if result.MaxScore > 0 {
		result.Score = result.RawScore * 100 / result.MaxScore
	}

	return result
}

type ProcessConfig struct {
	ProcessNames        []string
	ProcessWeight       int
	TCPWeight           int
	UDPWeight           int
	DNSWeight           int
	DNSEnabled          bool
	DNSDomain           string
	DNSDomains          []string
	DNSRecordType       string
	DNSLatencyThreshold time.Duration
	BindAddress         string
	Timeout             time.Duration
}
