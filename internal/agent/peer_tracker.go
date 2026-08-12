package agent

// peerTracker decides when a peer transition is worth alerting on.
//
// A peer must fail `threshold` consecutive polls before it is declared DOWN,
// which absorbs a single blip (one timeout / RTO) without waking anyone up.
//
// The previous reachability MUST be read before it is overwritten: an earlier
// version updated the stored value first, so by the time the failure streak
// reached the threshold the "was previously up" test could never pass and the
// DOWN alert never fired at all. The Update method keeps that ordering in one
// place so it stays testable.
type peerTracker struct {
	lastOK    map[string]bool
	downCount map[string]int
	lastCarp  map[string]string // CARP state the peer last reported while healthy
	threshold int
}

func newPeerTracker(threshold int) *peerTracker {
	if threshold < 1 {
		threshold = 1
	}
	return &peerTracker{
		lastOK:    make(map[string]bool),
		downCount: make(map[string]int),
		lastCarp:  make(map[string]string),
		threshold: threshold,
	}
}

// rememberCarp records the CARP state a healthy peer reported, so an alert
// fired while it is unreachable can say whether it holds the VIP.
func (t *peerTracker) rememberCarp(ip, carp string) {
	if carp != "" {
		t.lastCarp[ip] = carp
	}
}

// LastCarp returns the CARP state the peer last reported while healthy.
func (t *peerTracker) LastCarp(ip string) string {
	return t.lastCarp[ip]
}

// KnownDown reports whether the peer is in a confirmed-down state, i.e. its
// failure streak has crossed the threshold at least once.
func (t *peerTracker) KnownDown(ip string) bool {
	ok, seen := t.lastOK[ip]
	return seen && !ok
}

// Update records the result of one poll and reports which alert, if any, should
// fire. Each DOWN/UP transition fires at most once; a peer that stays down or
// stays up returns false for both.
func (t *peerTracker) Update(ip string, ok bool) (down, up bool) {
	wasOK, seen := t.lastOK[ip]

	if ok {
		t.downCount[ip] = 0
		t.lastOK[ip] = true
		// A peer first seen as reachable is not a "recovery".
		return false, seen && !wasOK
	}

	t.downCount[ip]++

	// Never seen reachable: the outage started before we were watching, so
	// stay quiet — but remember it, so the eventual recovery does alert.
	if !seen {
		t.lastOK[ip] = false
		return false, false
	}

	// While the streak is still below the threshold the peer is not declared
	// DOWN yet, so its recorded state stays "up". Marking it down here is what
	// used to swallow the alert: the next poll would no longer see a peer that
	// "was previously up", and the threshold could never be met.
	if wasOK && t.downCount[ip] >= t.threshold {
		t.downCount[ip] = 0
		t.lastOK[ip] = false
		return true, false
	}
	return false, false
}
