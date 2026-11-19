package security

import (
	"sync"
	"time"
)

// Limit is a fixed-window rate limit: Max events per Window.
type Limit struct {
	Max    int
	Window time.Duration
}

// Limiter is an in-process fixed-window limiter suitable for a single instance (§16.1).
// Multi-instance deployments must replace it with a shared store.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	count   int
	resetAt time.Time
}

// NewLimiter creates an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, now: time.Now}
}

// Allow records one event for key and reports whether it is within the limit.
// When denied it returns how long until the window resets.
func (l *Limiter) Allow(key string, lim Limit) (bool, time.Duration) {
	if lim.Max <= 0 || lim.Window <= 0 {
		return false, time.Minute // fail closed on misconfiguration
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b := l.buckets[key]
	if b == nil || !now.Before(b.resetAt) {
		b = &bucket{resetAt: now.Add(lim.Window)}
		l.buckets[key] = b
	}
	if b.count >= lim.Max {
		return false, b.resetAt.Sub(now)
	}
	b.count++
	return true, 0
}

// Reset clears a key (for example after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	for k, b := range l.buckets {
		if !now.Before(b.resetAt) {
			delete(l.buckets, k)
		}
	}
}
