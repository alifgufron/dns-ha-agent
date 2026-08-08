package health

import (
	"net"
	"time"

	"github.com/miekg/dns"
)

// CheckUDP verifies the DNS server answers over UDP. It only checks that a
// well-formed DNS response comes back — the rcode is irrelevant here, so an
// authoritative-only server (e.g. BIND9 refusing recursion) still passes.
// domain should match health.dns_query.domain so authoritative-only servers
// are queried for a zone they actually serve.
func CheckUDP(address, domain string, timeout time.Duration) bool {
	if address == "" {
		address = "127.0.0.1:53"
	}
	if domain == "" {
		domain = "google.com"
	}

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	m.RecursionDesired = true

	query, err := m.Pack()
	if err != nil {
		return false
	}

	conn, err := net.DialTimeout("udp", address, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}

	if _, err := conn.Write(query); err != nil {
		return false
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return false
	}

	// Verify we got at least a valid DNS response header (12+ bytes)
	return n >= 12
}
