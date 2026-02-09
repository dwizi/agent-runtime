package tui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/carlos/spinner/internal/adminclient"
	"github.com/carlos/spinner/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRecoverInvalidTLSConfigClearsInvalidPair(t *testing.T) {
	tempDir := t.TempDir()
	invalidCert := filepath.Join(tempDir, "invalid.crt")
	invalidKey := filepath.Join(tempDir, "invalid.key")
	if err := os.WriteFile(invalidCert, []byte("not-a-cert"), 0o644); err != nil {
		t.Fatalf("write invalid cert: %v", err)
	}
	if err := os.WriteFile(invalidKey, []byte("not-a-key"), 0o644); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}

	cfg := config.Config{
		AdminTLSCertFile: invalidCert,
		AdminTLSKeyFile:  invalidKey,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	updated, _ := recoverInvalidTLSConfig(cfg, "", logger)
	if updated.AdminTLSCertFile != "" || updated.AdminTLSKeyFile != "" {
		t.Fatal("expected invalid client cert config to be cleared")
	}
}

func TestTogglesToObjectivesMode(t *testing.T) {
	m := model{
		mode: modePairings,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	typed := updated.(model)
	if typed.mode != modeObjectives {
		t.Fatalf("expected objectives mode, got %s", typed.mode)
	}
}

func TestTabCyclesToTasksMode(t *testing.T) {
	m := model{
		mode: modeObjectives,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	typed := updated.(model)
	if typed.mode != modeTasks {
		t.Fatalf("expected tasks mode, got %s", typed.mode)
	}
}

func TestObjectiveLoadedMessageUpdatesSelection(t *testing.T) {
	m := model{
		mode: modeObjectives,
		objectives: []adminclient.Objective{
			{ID: "obj-1", Title: "One"},
			{ID: "obj-2", Title: "Two"},
		},
		objectiveIndex: 5,
	}
	updated, _ := m.Update(objectivesLoadedMsg{
		items: []adminclient.Objective{
			{ID: "obj-3", Title: "Three", Active: true},
		},
	})
	typed := updated.(model)
	if len(typed.objectives) != 1 {
		t.Fatalf("expected one objective, got %d", len(typed.objectives))
	}
	if typed.objectiveIndex != 0 {
		t.Fatalf("expected objective index normalized to 0, got %d", typed.objectiveIndex)
	}
}

func TestTasksLoadedMessageUpdatesSelection(t *testing.T) {
	m := model{
		mode: modeTasks,
		tasks: []adminclient.Task{
			{ID: "task-1", Title: "One"},
			{ID: "task-2", Title: "Two"},
		},
		taskIndex: 7,
	}
	updated, _ := m.Update(tasksLoadedMsg{
		items: []adminclient.Task{
			{ID: "task-3", Title: "Three", Status: "failed"},
		},
	})
	typed := updated.(model)
	if len(typed.tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(typed.tasks))
	}
	if typed.taskIndex != 0 {
		t.Fatalf("expected task index normalized to 0, got %d", typed.taskIndex)
	}
}
