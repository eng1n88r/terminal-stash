package main

import (
	"testing"
	"time"
)

func TestHubBroadcastFansOut(t *testing.T) {
	h := NewHub()
	a := h.subscribe()
	b := h.subscribe()
	defer h.unsubscribe(a)
	defer h.unsubscribe(b)

	h.Broadcast(Event{Type: "deleted", ID: "x"})

	for name, ch := range map[string]chan Event{"a": a, "b": b} {
		select {
		case ev := <-ch:
			if ev.Type != "deleted" || ev.ID != "x" {
				t.Errorf("%s received wrong event: %+v", name, ev)
			}
		case <-time.After(time.Second):
			t.Errorf("%s did not receive the event", name)
		}
	}
}

func TestHubUnsubscribeClosesChannel(t *testing.T) {
	h := NewHub()
	ch := h.subscribe()
	h.unsubscribe(ch)
	if _, open := <-ch; open {
		t.Error("channel still open after unsubscribe")
	}
	h.unsubscribe(ch) // double-unsubscribe must not panic
	h.Broadcast(Event{Type: "deleted", ID: "y"})
}

func TestHubSlowConsumerDoesNotBlock(t *testing.T) {
	h := NewHub()
	slow := h.subscribe() // never read from
	defer h.unsubscribe(slow)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast(Event{Type: "deleted", ID: "z"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow consumer")
	}
	if n := len(slow); n > cap(slow) {
		t.Errorf("subscriber buffer overflowed: %d", n)
	}
}
