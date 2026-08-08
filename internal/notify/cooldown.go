package notify

import (
	"sync"
	"time"
)

type Cooldown struct {
	mu       sync.Mutex
	duration time.Duration
	lastSent map[string]time.Time
}

func NewCooldown(duration time.Duration) *Cooldown {
	return &Cooldown{
		duration: duration,
		lastSent: make(map[string]time.Time),
	}
}

func (c *Cooldown) Allow(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	last, ok := c.lastSent[key]
	if !ok {
		c.lastSent[key] = time.Now()
		return true
	}

	if time.Since(last) >= c.duration {
		c.lastSent[key] = time.Now()
		return true
	}

	return false
}
