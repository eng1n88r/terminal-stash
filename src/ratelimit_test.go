package main

import (
	"fmt"
	"testing"
	"time"
)

func TestLimiterAllowsUnderMax(t *testing.T) {
	l := newLoginLimiter(3, time.Minute)
	for i := 0; i < 2; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("blocked after %d failures", i)
		}
		l.fail("1.2.3.4")
	}
	if !l.allow("1.2.3.4") {
		t.Error("blocked below max failures")
	}
}

func TestLimiterBlocksAtMax(t *testing.T) {
	l := newLoginLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		l.fail("1.2.3.4")
	}
	if l.allow("1.2.3.4") {
		t.Error("allowed at max failures")
	}
}

func TestLimiterWindowExpires(t *testing.T) {
	l := newLoginLimiter(2, 30*time.Millisecond)
	l.fail("1.2.3.4")
	l.fail("1.2.3.4")
	if l.allow("1.2.3.4") {
		t.Fatal("not blocked at max")
	}
	time.Sleep(50 * time.Millisecond)
	if !l.allow("1.2.3.4") {
		t.Error("still blocked after window expired")
	}
	// A failure after expiry starts a fresh window rather than extending the old one.
	l.fail("1.2.3.4")
	if !l.allow("1.2.3.4") {
		t.Error("blocked after a single failure in a fresh window")
	}
}

func TestLimiterSuccessResets(t *testing.T) {
	l := newLoginLimiter(2, time.Minute)
	l.fail("1.2.3.4")
	l.fail("1.2.3.4")
	l.success("1.2.3.4")
	if !l.allow("1.2.3.4") {
		t.Error("still blocked after successful login")
	}
}

func TestLimiterIsolatesIPs(t *testing.T) {
	l := newLoginLimiter(1, time.Minute)
	l.fail("1.1.1.1")
	if l.allow("1.1.1.1") {
		t.Error("1.1.1.1 should be blocked")
	}
	if !l.allow("2.2.2.2") {
		t.Error("2.2.2.2 blocked by 1.1.1.1's failures")
	}
}

func TestLimiterSweep(t *testing.T) {
	l := newLoginLimiter(3, 10*time.Millisecond)
	for i := 0; i < 1500; i++ {
		l.fail(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	time.Sleep(20 * time.Millisecond)
	l.allow("9.9.9.9") // triggers the sweep
	l.mu.Lock()
	n := len(l.failures)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("sweep left %d expired entries", n)
	}
}
