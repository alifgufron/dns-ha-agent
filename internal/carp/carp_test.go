package carp

import "testing"

// Sample ifconfig output with two VHIDs on the same interface — the agent must
// report the state of the configured VHID, not simply the first carp line.
const multiVhidOutput = `vtnet1: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> metric 0 mtu 1500
	ether 00:a0:98:00:00:01
	inet 10.0.0.10 netmask 0xffffff80 broadcast 10.0.0.127 vhid 1
	inet 10.0.0.20 netmask 0xffffff80 broadcast 10.0.0.127 vhid 2
	inet6 fd00:1::100 prefixlen 96 vhid 1
	carp: BACKUP vhid 1 advbase 1 advskew 100
	carp: MASTER vhid 2 advbase 1 advskew 0
	media: Ethernet autoselect (10Gbase-T <full-duplex>)
	status: active
`

func TestParseCarpStateSelectsVhid(t *testing.T) {
	cases := []struct {
		vhid int
		want State
	}{
		{1, StateBackup},
		{2, StateMaster},
	}
	for _, tc := range cases {
		got := parseCarpState(multiVhidOutput, tc.vhid)
		if got != tc.want {
			t.Errorf("vhid %d: got %v, want %v", tc.vhid, got, tc.want)
		}
	}
}

func TestParseCarpStateInitIsUnknown(t *testing.T) {
	out := "\tcarp: INIT vhid 1 advbase 1 advskew 0\n"
	if got := parseCarpState(out, 1); got != StateUnknown {
		t.Errorf("INIT should map to UNKNOWN, got %v", got)
	}
}

func TestParseCarpStateFallbackWithoutVhid(t *testing.T) {
	out := "\tcarp: MASTER\n"
	if got := parseCarpState(out, 1); got != StateMaster {
		t.Errorf("fallback parse failed, got %v", got)
	}
}

func TestParseVIPDualStack(t *testing.T) {
	vips := parseVIPs(multiVhidOutput, 1)
	if len(vips) != 2 {
		t.Fatalf("expected 2 VIPs for vhid 1 (v4+v6), got %d: %v", len(vips), vips)
	}
	if vips[0] != "10.0.0.10" || vips[1] != "fd00:1::100" {
		t.Errorf("unexpected VIPs: %v", vips)
	}
}

func TestParseVIPOtherVhid(t *testing.T) {
	vips := parseVIPs(multiVhidOutput, 2)
	if len(vips) != 1 || vips[0] != "10.0.0.20" {
		t.Errorf("expected only 10.0.0.20 for vhid 2, got %v", vips)
	}
}

func TestParseAdvskew(t *testing.T) {
	if skew, ok := parseAdvskew(multiVhidOutput, 1); !ok || skew != 100 {
		t.Errorf("vhid 1 advskew = %d (ok=%v), want 100", skew, ok)
	}
	if skew, ok := parseAdvskew(multiVhidOutput, 2); !ok || skew != 0 {
		t.Errorf("vhid 2 advskew = %d (ok=%v), want 0", skew, ok)
	}
}
