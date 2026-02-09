package heartbeat

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StateStarting = "starting"
	StateHealthy  = "healthy"
	StateDegraded = "degraded"
	StateDisabled = "disabled"
	StateStopped  = "stopped"
	StateStale    = "stale"
)

type Reporter interface {
	Starting(component, message string)
	Beat(component, message string)
	Degrade(component, message string, err error)
	Disabled(component, message string)
	Stopped(component, message string)
}

type ComponentStatus struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	BaseState      string `json:"base_state"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	LastBeatAtUnix int64  `json:"last_beat_at_unix,omitempty"`
	UpdatedAtUnix  int64  `json:"updated_at_unix"`
	Stale          bool   `json:"stale,omitempty"`
}

type Snapshot struct {
	GeneratedAtUnix int64             `json:"generated_at_unix"`
	Overall         string            `json:"overall"`
	Components      []ComponentStatus `json:"components"`
}

type componentRecord struct {
	name       string
	state      string
	message    string
	lastError  string
	lastBeatAt time.Time
	updatedAt  time.Time
}

type Registry struct {
	mu         sync.RWMutex
	components map[string]componentRecord
}

func NewRegistry() *Registry {
	return &Registry{
		components: map[string]componentRecord{},
	}
}

func (r *Registry) Starting(component, message string) {
	r.setState(component, StateStarting, message, "")
}

func (r *Registry) Beat(component, message string) {
	name := normalizeComponent(component)
	if name == "" {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.components[name]
	record.name = name
	record.state = StateHealthy
	record.message = strings.TrimSpace(message)
	record.lastError = ""
	record.lastBeatAt = now
	record.updatedAt = now
	r.components[name] = record
}

func (r *Registry) Degrade(component, message string, err error) {
	errorText := ""
	if err != nil {
		errorText = strings.TrimSpace(err.Error())
	}
	r.setState(component, StateDegraded, message, errorText)
}

func (r *Registry) Disabled(component, message string) {
	r.setState(component, StateDisabled, message, "")
}

func (r *Registry) Stopped(component, message string) {
	r.setState(component, StateStopped, message, "")
}

func (r *Registry) setState(component, state, message, errorText string) {
	name := normalizeComponent(component)
	if name == "" {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.components[name]
	record.name = name
	record.state = normalizeState(state)
	record.message = strings.TrimSpace(message)
	record.lastError = strings.TrimSpace(errorText)
	record.updatedAt = now
	if record.lastBeatAt.IsZero() {
		record.lastBeatAt = now
	}
	r.components[name] = record
}

func (r *Registry) Snapshot(staleAfter time.Duration) Snapshot {
	now := time.Now().UTC()
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]ComponentStatus, 0, len(r.components))
	for _, record := range r.components {
		status := ComponentStatus{
			Name:      record.name,
			BaseState: normalizeState(record.state),
			Message:   record.message,
			Error:     record.lastError,
		}
		if !record.lastBeatAt.IsZero() {
			status.LastBeatAtUnix = record.lastBeatAt.Unix()
		}
		if !record.updatedAt.IsZero() {
			status.UpdatedAtUnix = record.updatedAt.Unix()
		}
		status.State = status.BaseState

		if staleAfter > 0 && canBecomeStale(status.BaseState) {
			reference := record.lastBeatAt
			if reference.IsZero() {
				reference = record.updatedAt
			}
			if !reference.IsZero() && now.Sub(reference) > staleAfter {
				status.State = StateStale
				status.Stale = true
			}
		}
		results = append(results, status)
	}

	sort.Slice(results, func(left, right int) bool {
		return results[left].Name < results[right].Name
	})

	return Snapshot{
		GeneratedAtUnix: now.Unix(),
		Overall:         computeOverall(results),
		Components:      results,
	}
}

func IsDegradedState(state string) bool {
	switch normalizeState(state) {
	case StateDegraded, StateStale:
		return true
	default:
		return false
	}
}

func normalizeComponent(component string) string {
	return strings.ToLower(strings.TrimSpace(component))
}

func normalizeState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case StateStarting:
		return StateStarting
	case StateHealthy:
		return StateHealthy
	case StateDegraded:
		return StateDegraded
	case StateDisabled:
		return StateDisabled
	case StateStopped:
		return StateStopped
	case StateStale:
		return StateStale
	default:
		return StateHealthy
	}
}

func canBecomeStale(state string) bool {
	switch normalizeState(state) {
	case StateHealthy, StateStarting:
		return true
	default:
		return false
	}
}

func computeOverall(items []ComponentStatus) string {
	if len(items) == 0 {
		return "unknown"
	}
	hasHealthy := false
	hasStarting := false
	allInactive := true
	for _, item := range items {
		switch normalizeState(item.State) {
		case StateDegraded, StateStale:
			return StateDegraded
		case StateHealthy:
			hasHealthy = true
			allInactive = false
		case StateStarting:
			hasStarting = true
			allInactive = false
		case StateDisabled, StateStopped:
		default:
			allInactive = false
		}
	}
	if hasStarting {
		return StateStarting
	}
	if hasHealthy {
		return StateHealthy
	}
	if allInactive {
		return "idle"
	}
	return StateHealthy
}
