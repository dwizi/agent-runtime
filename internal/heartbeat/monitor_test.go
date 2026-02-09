package heartbeat

import (
	"context"
	"testing"
	"time"
)

func TestMonitorEmitsTransitions(t *testing.T) {
	registry := NewRegistry()
	transitions := make(chan Transition, 4)
	monitor := NewMonitor(registry, MonitorConfig{
		Interval:   10 * time.Millisecond,
		StaleAfter: 0,
		OnTransition: func(ctx context.Context, transition Transition, snapshot Snapshot) {
			transitions <- transition
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = monitor.Start(ctx)
		close(done)
	}()

	registry.Beat("scheduler", "ok")
	time.Sleep(25 * time.Millisecond)
	registry.Degrade("scheduler", "queue stalled", context.DeadlineExceeded)

	var degraded Transition
	select {
	case degraded = <-transitions:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected degraded transition")
	}
	if degraded.FromState != StateHealthy || degraded.ToState != StateDegraded {
		t.Fatalf("unexpected degraded transition: %+v", degraded)
	}

	registry.Beat("scheduler", "recovered")
	var recovered Transition
	select {
	case recovered = <-transitions:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected recovered transition")
	}
	if recovered.FromState != StateDegraded || recovered.ToState != StateHealthy {
		t.Fatalf("unexpected recovered transition: %+v", recovered)
	}

	cancel()
	<-done
}
