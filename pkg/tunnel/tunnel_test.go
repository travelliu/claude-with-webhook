package tunnel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchURL_DetectsURLChange(t *testing.T) {
	var url atomic.Value
	url.Store("https://old.example.com")

	m := &Manager{
		getURLFunc: func() (string, error) {
			return url.Load().(string), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := m.WatchURL(ctx, 50*time.Millisecond)

	// Change the URL after a short delay
	go func() {
		time.Sleep(120 * time.Millisecond)
		url.Store("https://new.example.com")
	}()

	select {
	case got := <-ch:
		if got != "https://new.example.com" {
			t.Errorf("expected https://new.example.com, got %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for URL change")
	}
}

func TestWatchURL_NoEventOnSameURL(t *testing.T) {
	m := &Manager{
		getURLFunc: func() (string, error) {
			return "https://stable.example.com", nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := m.WatchURL(ctx, 50*time.Millisecond)

	// Wait long enough for several ticks
	time.Sleep(300 * time.Millisecond)

	select {
	case <-ch:
		t.Fatal("received unexpected URL change event")
	default:
		// expected: no event
	}
}

func TestWatchURL_ChannelClosesOnCancel(t *testing.T) {
	m := &Manager{
		getURLFunc: func() (string, error) {
			return "https://example.com", nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch := m.WatchURL(ctx, 50*time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after cancel")
	}
}

func TestWatchURL_InitialURLErrorIsIgnored(t *testing.T) {
	var calls atomic.Int32
	m := &Manager{
		getURLFunc: func() (string, error) {
			n := calls.Add(1)
			if n == 1 {
				return "", context.DeadlineExceeded
			}
			return "https://recovered.example.com", nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := m.WatchURL(ctx, 50*time.Millisecond)

	select {
	case got := <-ch:
		if got != "https://recovered.example.com" {
			t.Errorf("expected https://recovered.example.com, got %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for URL recovery")
	}
}

func TestWatchURL_MultipleChanges(t *testing.T) {
	var url atomic.Value
	url.Store("https://v1.example.com")

	m := &Manager{
		getURLFunc: func() (string, error) {
			return url.Load().(string), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := m.WatchURL(ctx, 50*time.Millisecond)

	// Change URL twice
	go func() {
		time.Sleep(120 * time.Millisecond)
		url.Store("https://v2.example.com")
		time.Sleep(100 * time.Millisecond)
		url.Store("https://v3.example.com")
	}()

	// Collect first change
	select {
	case got := <-ch:
		if got != "https://v2.example.com" {
			t.Errorf("first change: expected https://v2.example.com, got %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first URL change")
	}

	// Collect second change
	select {
	case got := <-ch:
		if got != "https://v3.example.com" {
			t.Errorf("second change: expected https://v3.example.com, got %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second URL change")
	}
}
