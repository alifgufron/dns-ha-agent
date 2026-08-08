package carp

import (
	"fmt"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/util"
)

func InterfaceDown(iface string) error {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface, "down")
	if result.Err != nil {
		return fmt.Errorf("ifconfig %s down: %w (stderr: %s)", iface, result.Err, result.Stderr)
	}
	return nil
}

func InterfaceUp(iface string) error {
	result := util.ExecTimeout(5*time.Second, "ifconfig", iface, "up")
	if result.Err != nil {
		return fmt.Errorf("ifconfig %s up: %w (stderr: %s)", iface, result.Err, result.Stderr)
	}
	return nil
}
