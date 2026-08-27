package health

import (
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type DNSResult struct {
	Success bool          `json:"success"`
	RTT     time.Duration `json:"rtt"`
	Error   string        `json:"error,omitempty"`
	Slow    bool          `json:"slow,omitempty"` // true if query succeeded but RTT exceeded LatencyThreshold
}

func CheckDNS(domain, resolver string, timeout time.Duration) DNSResult {
	return CheckDNSQuery(domain, "A", resolver, timeout)
}

func CheckDNSQuery(domain, recordType, resolver string, timeout time.Duration) DNSResult {
	if resolver == "" {
		resolver = "127.0.0.1:53"
	}
	if domain == "" {
		domain = "google.com"
	}
	if recordType == "" {
		recordType = "A"
	}

	qtype, ok := dns.StringToType[strings.ToUpper(recordType)]
	if !ok {
		qtype = dns.TypeA
	}

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)

	c := new(dns.Client)
	c.Timeout = timeout

	start := time.Now()
	r, rtt, err := c.Exchange(m, resolver)
	elapsed := time.Since(start)

	if err != nil {
		return DNSResult{
			Success: false,
			RTT:     elapsed,
			Error:   fmt.Sprintf("exchange failed: %v", err),
		}
	}

	if r == nil {
		return DNSResult{
			Success: false,
			RTT:     elapsed,
			Error:   "nil response",
		}
	}

	if r.Rcode != dns.RcodeSuccess {
		return DNSResult{
			Success: false,
			RTT:     rtt,
			Error:   fmt.Sprintf("rcode: %s", dns.RcodeToString[r.Rcode]),
		}
	}

	return DNSResult{
		Success: true,
		RTT:     rtt,
	}
}
