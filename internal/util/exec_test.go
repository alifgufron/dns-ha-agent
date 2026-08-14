package util

import (
	"strings"
	"testing"
	"time"
)

func TestPingRTT(t *testing.T) {
	cases := map[string]string{
		"PING 10.0.0.1 (10.0.0.1): 56 data bytes\n64 bytes from 10.0.0.1: icmp_seq=0 ttl=64 time=0.458 ms\n\n--- 10.0.0.1 ping statistics ---\n1 packets transmitted, 1 packets received, 0.0% packet loss\nround-trip min/avg/max/stddev = 0.458/0.458/0.458/0.000 ms\n": "0.458 ms",
		"64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=0.035 ms":                                                   "0.035 ms",
		"PING 10.0.0.1 (10.0.0.1): 56 data bytes\n\n1 packets transmitted, 0 packets received, 100.0% packet loss\n": "",
		"": "",
	}
	for out, want := range cases {
		if got := pingRTT(out); got != want {
			t.Errorf("pingRTT(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestPingHostLocalhost(t *testing.T) {
	ok, detail := PingHost("127.0.0.1", 2*time.Second)
	if !ok {
		t.Fatalf("PingHost(127.0.0.1) failed: %q", detail)
	}
	if !strings.Contains(detail, "ms") {
		t.Errorf("expected an RTT detail, got %q", detail)
	}
}
