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

	addresses := cfg.BindAddresses
	if len(addresses) == 0 {
		if cfg.BindAddress != "" {
			addresses = []string{cfg.BindAddress}
		} else {
			addresses = []string{"127.0.0.1:53"}
		}
	}

	result := HealthResult{
		MaxScore: w.Process + w.TCP + w.UDP + w.DNS,
	}

	result.ProcessAlive = CheckProcesses(cfg.ProcessNames, timeout)
	if result.ProcessAlive {
		result.RawScore += w.Process
	}

	allTCP := true
	for _, addr := range addresses {
		if !CheckTCP(addr, timeout) {
			allTCP = false
			break
		}
	}
	result.TCPAlive = allTCP
	if result.TCPAlive {
		result.RawScore += w.TCP
	}

	udpDomain := cfg.DNSDomain
	if udpDomain == "" && len(cfg.DNSDomains) > 0 {
		udpDomain = cfg.DNSDomains[0]
	}
	allUDP := true
	for _, addr := range addresses {
		if !CheckUDP(addr, udpDomain, timeout) {
			allUDP = false
			break
		}
	}
	result.UDPAlive = allUDP
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
		var queryCount int
		var lastDetail DNSResult

		for _, addr := range addresses {
			for _, dom := range domains {
				res := CheckDNSQuery(dom, cfg.DNSRecordType, addr, timeout)
				if !res.Success {
					allSuccess = false
					lastDetail = res
					break
				}
				totalRTT += res.RTT
				queryCount++
				if cfg.DNSLatencyThreshold > 0 && res.RTT > cfg.DNSLatencyThreshold {
					anySlow = true
				}
				lastDetail = res
			}
			if !allSuccess {
				break
			}
		}

		if queryCount > 0 {
			lastDetail.RTT = totalRTT / time.Duration(queryCount)
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
	BindAddresses       []string
	Timeout             time.Duration
}
