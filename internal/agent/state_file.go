package agent

import (
	"os"
	"strings"
)

// stateFileFormat is the on-disk format for the last-known agent state,
// so a restart resumes cleanly without re-notifying a stale transition.
const stateFileFormat = "dns-ha-agent state v1\nstate=%s\n"

func loadState(path string) (State, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return StateHealthy, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "state=") {
			switch strings.TrimSpace(strings.TrimPrefix(line, "state=")) {
			case StateHealthy.String():
				return StateHealthy, true
			case StateDegraded.String():
				return StateDegraded, true
			case StateUnhealthy.String():
				return StateUnhealthy, true
			}
		}
	}
	return StateHealthy, false
}

func saveState(path string, s State) error {
	data := []byte(strings.Replace(stateFileFormat, "%s", s.String(), 1))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
