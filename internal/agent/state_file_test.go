package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")

	if _, ok := loadState(path); ok {
		t.Fatal("loadState should report false for missing file")
	}

	for _, s := range []State{StateHealthy, StateDegraded, StateUnhealthy} {
		if err := saveState(path, s); err != nil {
			t.Fatalf("saveState(%v): %v", s, err)
		}
		got, ok := loadState(path)
		if !ok {
			t.Fatalf("loadState after save(%v): not ok", s)
		}
		if got != s {
			t.Fatalf("round trip mismatch: saved %v, loaded %v", s, got)
		}
	}
}

func TestLoadStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("garbage\nno state here\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadState(path); ok {
		t.Fatal("corrupt file should not load as a valid state")
	}
}
