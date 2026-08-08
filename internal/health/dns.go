package health

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
)

type DNSResult struct {
	Success bool
	RTT     time.Duration
	Error   string
}

func CheckDNS(domain, resolver string, timeout time.Duration) DNSResult {
	if resolver == "" {
		resolver = "127.0.0.1:53"
	}
	if domain == "" {
		domain = "google.com"
	}

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)

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
