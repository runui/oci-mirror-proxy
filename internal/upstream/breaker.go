// Package upstream turns upstream definitions into runnable candidates: each one
// has its own transport, authorizer, circuit breaker and statistics. The Pool
// keeps those candidates in sync with configuration.
package upstream

import (
	"sync"
	"time"
)

// State is the circuit state of an upstream.
type State int

const (
	// Closed means requests flow normally.
	Closed State = iota
	// Open means requests are rejected until the cooldown expires.
	Open
	// HalfOpen means a single probe request is allowed to test recovery.
	HalfOpen
)

// String returns the lower-case name used in status output.
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker is a per-upstream circuit breaker.
//
// Every successful Allow must be followed by exactly one of Success, Failure or
// Abort. Otherwise a half-open probe would stay in flight forever and block the
// upstream permanently.
type Breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time

	failures  int
	openUntil time.Time // zero value means the circuit is closed
	probing   bool      // a half-open probe is currently in flight
}

// NewBreaker creates a closed breaker. The clock is injected so tests can control time.
func NewBreaker(threshold int, cooldown time.Duration, now func() time.Time) *Breaker {
	return &Breaker{threshold: threshold, cooldown: cooldown, now: now}
}

// Allow reports whether a request may be sent now.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if b.now().Before(b.openUntil) || b.probing {
		return false
	}
	// Cooldown has passed and no probe is running: this caller becomes the probe.
	b.probing = true
	return true
}

// Success closes the circuit and clears the failure count.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.probing = false
}

// Failure records a failed attempt and opens the circuit when the threshold is
// reached. A failed probe reopens it immediately.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.probing || b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
	b.probing = false
}

// Abort releases a probe without judging the upstream. It is used when the
// caller cancelled the request, which says nothing about upstream health.
func (b *Breaker) Abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

// State reports the current circuit state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.openUntil.IsZero():
		return Closed
	case b.now().Before(b.openUntil):
		return Open
	default:
		return HalfOpen
	}
}
