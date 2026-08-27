package agent

import "testing"

const testPeerIP = "10.0.0.10"

// Reproduces the reported failure: VM A was healthy, then shut down, and VM B
// logged "peer unreachable" every cycle but never sent a DOWN alert — only the
// later "UP (recovered)" mail arrived.
func TestPeerDownAlertFiresAfterThreshold(t *testing.T) {
	tr := newPeerTracker(2)
	tr.Update(testPeerIP, true) // peer known good first

	if down, _ := tr.Update(testPeerIP, false); down {
		t.Error("first failure must not alert — that is the blip we absorb")
	}
	down, _ := tr.Update(testPeerIP, false)
	if !down {
		t.Fatal("DOWN alert must fire once the failure streak reaches the threshold")
	}
}

func TestPeerDownAlertFiresOnlyOnce(t *testing.T) {
	tr := newPeerTracker(2)
	tr.Update(testPeerIP, true)
	tr.Update(testPeerIP, false)
	tr.Update(testPeerIP, false) // fires here

	for i := 0; i < 5; i++ {
		if down, _ := tr.Update(testPeerIP, false); down {
			t.Fatalf("DOWN re-fired on poll %d while the peer stayed down", i+3)
		}
	}
}

func TestPeerUpAlertAfterRecovery(t *testing.T) {
	tr := newPeerTracker(2)
	tr.Update(testPeerIP, true)
	tr.Update(testPeerIP, false)
	tr.Update(testPeerIP, false)

	_, up := tr.Update(testPeerIP, true)
	if !up {
		t.Fatal("UP alert must fire when the peer comes back")
	}
	if _, up := tr.Update(testPeerIP, true); up {
		t.Error("UP must not re-fire while the peer stays up")
	}
}

// A full down -> up -> down cycle must alert again; the streak state from the
// first outage must not suppress the second one.
func TestPeerDownAlertFiresAgainAfterRecovery(t *testing.T) {
	tr := newPeerTracker(2)
	tr.Update(testPeerIP, true)
	tr.Update(testPeerIP, false)
	tr.Update(testPeerIP, false) // DOWN #1
	tr.Update(testPeerIP, true)  // UP

	tr.Update(testPeerIP, false)
	if down, _ := tr.Update(testPeerIP, false); !down {
		t.Fatal("DOWN must fire again for a second outage")
	}
}

// A peer that is already unreachable when the agent starts should not generate
// an alert for an outage that began before we were watching.
func TestPeerDownAtStartupDoesNotAlert(t *testing.T) {
	tr := newPeerTracker(2)
	for i := 0; i < 5; i++ {
		if down, up := tr.Update(testPeerIP, false); down || up {
			t.Fatalf("unseen peer must not alert on poll %d (down=%v up=%v)", i+1, down, up)
		}
	}
	// ...but its recovery is still worth reporting.
	if _, up := tr.Update(testPeerIP, true); !up {
		t.Error("recovery of a peer down since startup should alert")
	}
}

func TestPeerTrackerIsolatesPeers(t *testing.T) {
	tr := newPeerTracker(2)
	const other = "10.0.0.20"
	tr.Update(testPeerIP, true)
	tr.Update(other, true)

	tr.Update(testPeerIP, false)
	if down, _ := tr.Update(other, false); down {
		t.Error("one failure per peer must not be combined into a shared streak")
	}
}

// A severity change while a peer is already down (e.g. a VM shutting down moves
// from "connection refused" to "no reply") must be visible to the runner so it
// can fire a fresh notification with the new classification.
func TestPeerStatusChangeWhileDown(t *testing.T) {
	tr := newPeerTracker(2)
	tr.Update(testPeerIP, true)
	tr.Update(testPeerIP, false)
	tr.Update(testPeerIP, false) // declared DOWN

	if s := tr.Status(testPeerIP); s != "" {
		t.Fatalf("status should be unset before first notify, got %q", s)
	}

	tr.SetStatus(testPeerIP, "CRITICAL (host up, DNS not serving)")
	if !tr.KnownDown(testPeerIP) {
		t.Fatal("peer must be KnownDown after being declared down")
	}
	if s := tr.Status(testPeerIP); s != "CRITICAL (host up, DNS not serving)" {
		t.Fatalf("Status = %q, want CRITICAL", s)
	}

	// Same status again → no change.
	if s := tr.Status(testPeerIP); s == "DOWN (host unreachable)" {
		t.Fatal("status must not silently mutate")
	}

	tr.SetStatus(testPeerIP, "DOWN (host unreachable)")
	if s := tr.Status(testPeerIP); s != "DOWN (host unreachable)" {
		t.Fatalf("Status after change = %q, want DOWN", s)
	}

	// Recovery clears the recorded status.
	tr.Update(testPeerIP, true)
	if s := tr.Status(testPeerIP); s != "" {
		t.Errorf("status must clear on recovery, got %q", s)
	}
}
