package app

import (
	"sync"
	"time"
)

type rateWindow struct {
	started time.Time
	count   int
}

type fixedWindowLimiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

func newFixedWindowLimiter() *fixedWindowLimiter {
	return &fixedWindowLimiter{windows: make(map[string]rateWindow)}
}

func (l *fixedWindowLimiter) allow(key string, limit int, duration time.Duration) bool {
	if limit <= 0 {
		return false
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	window, ok := l.windows[key]
	if !ok || now.Sub(window.started) >= duration {
		l.windows[key] = rateWindow{started: now, count: 1}
		l.pruneLocked(now, duration)
		return true
	}
	if window.count >= limit {
		return false
	}
	window.count++
	l.windows[key] = window
	return true
}

func (l *fixedWindowLimiter) pruneLocked(now time.Time, duration time.Duration) {
	if len(l.windows) < 2048 {
		return
	}
	for key, window := range l.windows {
		if now.Sub(window.started) >= duration {
			delete(l.windows, key)
		}
	}
}
