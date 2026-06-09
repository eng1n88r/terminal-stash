package main

import (
	"sync"
	"time"
)

// loginLimiter throttles failed login attempts per client IP using a fixed
// window. A successful login clears the counter for that IP.
type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	failures map[string]*failWindow
}

type failWindow struct {
	count int
	start time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, failures: make(map[string]*failWindow)}
}

// allow reports whether ip may attempt a login right now.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()
	w, ok := l.failures[ip]
	if !ok || time.Since(w.start) > l.window {
		return true
	}
	return w.count < l.max
}

// fail records a failed attempt for ip.
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w, ok := l.failures[ip]; ok && time.Since(w.start) <= l.window {
		w.count++
		return
	}
	l.failures[ip] = &failWindow{count: 1, start: time.Now()}
}

// success clears the failure counter for ip.
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

// sweepLocked drops expired windows so the map can't grow unbounded under a
// spread-out brute-force attempt. Called with l.mu held.
func (l *loginLimiter) sweepLocked() {
	if len(l.failures) < 1024 {
		return
	}
	for ip, w := range l.failures {
		if time.Since(w.start) > l.window {
			delete(l.failures, ip)
		}
	}
}
