package heartbeat

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

type Transition struct {
	Component string `json:"component"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

type MonitorConfig struct {
	Interval     time.Duration
	StaleAfter   time.Duration
	Logger       *slog.Logger
	OnTransition func(context.Context, Transition, Snapshot)
}

type Monitor struct {
	registry     *Registry
	interval     time.Duration
	staleAfter   time.Duration
	logger       *slog.Logger
	onTransition func(context.Context, Transition, Snapshot)
}

func NewMonitor(registry *Registry, cfg MonitorConfig) *Monitor {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		registry:     registry,
		interval:     interval,
		staleAfter:   cfg.StaleAfter,
		logger:       logger,
		onTransition: cfg.OnTransition,
	}
}

func (m *Monitor) Start(ctx context.Context) error {
	if m.registry == nil {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	m.logger.Info("heartbeat monitor started", "interval", m.interval.String(), "stale_after", m.staleAfter.String())

	previous := map[string]string{}
	for {
		snapshot := m.registry.Snapshot(m.staleAfter)
		m.evaluateTransitions(ctx, snapshot, previous)
		if ctx.Err() != nil {
			m.logger.Info("heartbeat monitor stopped")
			return nil
		}
		select {
		case <-ctx.Done():
			m.logger.Info("heartbeat monitor stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (m *Monitor) evaluateTransitions(ctx context.Context, snapshot Snapshot, previous map[string]string) {
	for _, item := range snapshot.Components {
		current := strings.ToLower(strings.TrimSpace(item.State))
		if current == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		before, seen := previous[name]
		previous[name] = current
		if !seen || before == current {
			continue
		}
		if m.onTransition != nil {
			transition := Transition{
				Component: name,
				FromState: before,
				ToState:   current,
				Message:   strings.TrimSpace(item.Message),
				Error:     strings.TrimSpace(item.Error),
			}
			m.onTransition(ctx, transition, snapshot)
		}
	}
}
