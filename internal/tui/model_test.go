package tui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dwizi/agent-runtime/internal/adminclient"
	"github.com/dwizi/agent-runtime/internal/config"
)

func keyPress(code rune, text string, mods ...tea.KeyMod) tea.KeyPressMsg {
	var mod tea.KeyMod
	for _, item := range mods {
		mod |= item
	}
	return tea.KeyPressMsg(tea.Key{
		Code: code,
		Text: text,
		Mod:  mod,
	})
}

func keyRune(r rune) tea.KeyPressMsg {
	return keyPress(r, string(r))
}

func newTestModel() model {
	cfg := config.Config{
		Environment:       "test",
		AdminAPIURL:       "https://admin.test",
		TUIApproverUserID: "tui-admin",
		TUIApprovalRole:   "admin",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := newModel(cfg, "", nil, logger)
	m.width = 140
	m.height = 48
	m.resizeWidgets()
	return m
}

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

func TestTabCyclesFocusZones(t *testing.T) {
	m := newTestModel()
	if m.focus != focusSidebar {
		t.Fatalf("expected initial sidebar focus, got %d", m.focus)
	}
	updated, _ := m.Update(keyPress(tea.KeyTab, ""))
	typed := updated.(model)
	if typed.focus != focusWorkbench {
		t.Fatalf("expected workbench focus, got %d", typed.focus)
	}
	updated, _ = typed.Update(keyPress(tea.KeyTab, ""))
	typed = updated.(model)
	if typed.focus != focusInspector {
		t.Fatalf("expected inspector focus, got %d", typed.focus)
	}
}

func TestShiftTabCyclesFocusBackward(t *testing.T) {
	m := newTestModel()
	m.focus = focusWorkbench
	updated, _ := m.Update(keyPress(tea.KeyTab, "", tea.ModShift))
	typed := updated.(model)
	if typed.focus != focusSidebar {
		t.Fatalf("expected sidebar focus, got %d", typed.focus)
	}
}

func TestNumericViewSwitch(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(keyRune('4'))
	typed := updated.(model)
	if typed.activeView != viewTasks {
		t.Fatalf("expected active view %s, got %s", viewTasks, typed.activeView)
	}
	if typed.sidebarIndex != sidebarIndexForView(viewTasks) {
		t.Fatalf("expected sidebar index %d, got %d", sidebarIndexForView(viewTasks), typed.sidebarIndex)
	}
}

func TestWindowResizeUpdatesDimensions(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 52})
	typed := updated.(model)
	if typed.width != 160 || typed.height != 52 {
		t.Fatalf("expected dimensions 160x52, got %dx%d", typed.width, typed.height)
	}
}

func TestPairingsRoleCycleForwardAndBackward(t *testing.T) {
	m := newTestModel()
	m.activeView = viewPairings
	m.focus = focusWorkbench
	_ = m.applyFocusCmd()
	updated, _ := m.Update(keyRune(']'))
	typed := updated.(model)
	if typed.pairingRole != "operator" {
		t.Fatalf("expected operator role, got %s", typed.pairingRole)
	}

	updated, _ = typed.Update(keyRune('['))
	typed = updated.(model)
	if typed.pairingRole != "admin" {
		t.Fatalf("expected admin role, got %s", typed.pairingRole)
	}
}

func TestPairingTokenInputSanitizes(t *testing.T) {
	m := newTestModel()
	m.activeView = viewPairings
	m.focus = focusWorkbench
	_ = m.applyFocusCmd()
	_ = m.tokenInput.Focus()

	typed := m
	for _, r := range "t6-*_z" {
		updated, _ := typed.Update(keyRune(r))
		typed = updated.(model)
	}
	if typed.tokenInput.Value() != "T6-Z" {
		t.Fatalf("expected sanitized token T6-Z, got %s", typed.tokenInput.Value())
	}
}

func TestObjectivesLoadedMessageUpdatesSelection(t *testing.T) {
	m := newTestModel()
	m.objectives = []adminclient.Objective{{ID: "obj-1", Title: "One"}, {ID: "obj-2", Title: "Two"}}
	m.rebuildObjectiveRows()
	m.objectivesTable.SetCursor(5)
	m.objectiveWorkspaceInput.SetValue("ws-1")

	updated, _ := m.Update(objectivesLoadedMsg{
		items:       []adminclient.Objective{{ID: "obj-3", Title: "Three", Active: true}},
		workspaceID: "ws-1",
	})
	typed := updated.(model)
	if len(typed.objectives) != 1 {
		t.Fatalf("expected one objective, got %d", len(typed.objectives))
	}
	if typed.objectivesTable.Cursor() != 0 {
		t.Fatalf("expected objective cursor normalized to 0, got %d", typed.objectivesTable.Cursor())
	}
}

func TestTasksLoadedMessageUpdatesSelection(t *testing.T) {
	m := newTestModel()
	m.tasks = []adminclient.Task{{ID: "task-1", Title: "One"}, {ID: "task-2", Title: "Two"}}
	m.rebuildTaskRows()
	m.tasksTable.SetCursor(7)
	m.taskWorkspaceInput.SetValue("ws-1")
	m.taskStatusFilter = ""

	updated, _ := m.Update(tasksLoadedMsg{
		items:       []adminclient.Task{{ID: "task-3", Title: "Three", Status: "failed"}},
		workspaceID: "ws-1",
		status:      "",
	})
	typed := updated.(model)
	if len(typed.tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(typed.tasks))
	}
	if typed.tasksTable.Cursor() != 0 {
		t.Fatalf("expected task cursor normalized to 0, got %d", typed.tasksTable.Cursor())
	}
}

func TestTaskFilterCycleForward(t *testing.T) {
	m := newTestModel()
	m.activeView = viewTasks
	m.focus = focusWorkbench
	m.taskWorkspaceInput.SetValue("ws-1")
	_ = m.applyFocusCmd()

	updated, _ := m.Update(keyRune(']'))
	typed := updated.(model)
	if typed.taskStatusFilter != "failed" {
		t.Fatalf("expected failed filter, got %s", typed.taskStatusFilter)
	}
}

func TestRetryOnlyFailedTask(t *testing.T) {
	m := newTestModel()
	m.activeView = viewTasks
	m.focus = focusWorkbench
	m.tasks = []adminclient.Task{{ID: "task-1", Title: "Task", Status: "running"}}
	m.rebuildTaskRows()
	_ = m.applyFocusCmd()

	updated, _ := m.Update(keyRune('y'))
	typed := updated.(model)
	if typed.errorText != "only failed tasks can be retried" {
		t.Fatalf("expected failed-only retry error, got %s", typed.errorText)
	}
}

func TestNormalizePairingRoleFallback(t *testing.T) {
	role := normalizePairingRole("unknown")
	if role != "admin" {
		t.Fatalf("expected fallback admin role, got %s", role)
	}
}
