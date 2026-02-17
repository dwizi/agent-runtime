package tui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/dwizi/agent-runtime/internal/adminclient"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/envsync"
)

type viewID string

const (
	viewOverview   viewID = "overview"
	viewPairings   viewID = "pairings"
	viewObjectives viewID = "objectives"
	viewTasks      viewID = "tasks"
	viewActivity   viewID = "activity"
)

type focusZone int

const (
	focusSidebar focusZone = iota
	focusWorkbench
	focusInspector
	focusHelp
)

type activityEvent struct {
	At      time.Time
	Level   string
	Message string
}

type dashboardStats struct {
	ObjectivesTotal  int
	ObjectivesActive int
	ObjectivesFailed int
	TasksTotal       int
	TasksQueued      int
	TasksRunning     int
	TasksFailed      int
	TasksSucceeded   int
	LastRefresh      time.Time
}

type model struct {
	cfg    config.Config
	logger *slog.Logger
	client *adminclient.Client

	width  int
	height int

	clock time.Time
	frame int

	quitting bool

	activeView   viewID
	sidebarIndex int
	focus        focusZone

	keys keyMap
	help help.Model

	spinner spinner.Model

	pendingLoads     int
	pendingMutations int

	statusText  string
	errorText   string
	startupInfo string

	pairingRole string

	tokenInput  textinput.Model
	activePair  *adminclient.Pairing
	approvedMsg *adminclient.ApprovePairingResponse

	objectiveWorkspaceInput textinput.Model
	objectives              []adminclient.Objective
	objectivesTable         table.Model

	taskWorkspaceInput textinput.Model
	taskStatusFilter   string
	tasks              []adminclient.Task
	tasksTable         table.Model
	taskRetryMsg       *adminclient.RetryTaskResponse

	inspectorViewport viewport.Model
	activityViewport  viewport.Model

	showFullHelp bool

	dashboard dashboardStats
	activity  []activityEvent

	debounceSequence int
}

type tickMsg struct {
	at time.Time
}

type pollMsg struct {
	at time.Time
}

type bootstrapMsg struct{}

type workspaceDebounceMsg struct {
	seq    int
	target viewID
	value  string
}

func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(at time.Time) tea.Msg {
		return tickMsg{at: at}
	})
}

func pollCmd() tea.Cmd {
	return tea.Tick(8*time.Second, func(at time.Time) tea.Msg {
		return pollMsg{at: at}
	})
}

func bootstrapCmd() tea.Cmd {
	return func() tea.Msg {
		return bootstrapMsg{}
	}
}

func workspaceDebounceCmd(seq int, target viewID, value string) tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return workspaceDebounceMsg{seq: seq, target: target, value: value}
	})
}

func Run(cfg config.Config, logger *slog.Logger) error {
	updatedCfg, startupInfo := syncEnvAtStartup(cfg, logger)

	client, err := adminclient.New(updatedCfg)
	if err != nil {
		return err
	}

	m := newModel(updatedCfg, startupInfo, client, logger)
	program := tea.NewProgram(m)
	_, err = program.Run()
	return err
}

func newModel(cfg config.Config, startupInfo string, client *adminclient.Client, logger *slog.Logger) model {
	keys := newKeyMap()

	helpModel := help.New()
	helpModel.ShowAll = false

	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	tokenInput := textinput.New()
	tokenInput.Prompt = "token> "
	tokenInput.Placeholder = "paste pairing token"
	tokenInput.CharLimit = 128
	tokenInput.SetValue("")

	objectiveWorkspaceInput := textinput.New()
	objectiveWorkspaceInput.Prompt = "workspace> "
	objectiveWorkspaceInput.Placeholder = "ws-1"
	objectiveWorkspaceInput.CharLimit = 128
	objectiveWorkspaceInput.SetValue("ws-1")

	taskWorkspaceInput := textinput.New()
	taskWorkspaceInput.Prompt = "workspace> "
	taskWorkspaceInput.Placeholder = "ws-1"
	taskWorkspaceInput.CharLimit = 128
	taskWorkspaceInput.SetValue("ws-1")

	objectivesTable := table.New()
	objectivesTable.Focus()
	objectivesTable.SetColumns([]table.Column{{Title: "Title", Width: 32}, {Title: "State", Width: 10}, {Title: "Trigger", Width: 12}, {Title: "Next Run", Width: 22}})

	tasksTable := table.New()
	tasksTable.Focus()
	tasksTable.SetColumns([]table.Column{{Title: "Title", Width: 36}, {Title: "Status", Width: 10}, {Title: "Kind", Width: 12}, {Title: "Attempts", Width: 10}, {Title: "Updated", Width: 22}})

	inspectorVP := viewport.New(viewport.WithWidth(40), viewport.WithHeight(20))
	activityVP := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))

	m := model{
		cfg:                     cfg,
		logger:                  logger,
		client:                  client,
		clock:                   time.Now().UTC(),
		activeView:              viewOverview,
		sidebarIndex:            0,
		focus:                   focusSidebar,
		keys:                    keys,
		help:                    helpModel,
		spinner:                 spin,
		startupInfo:             startupInfo,
		pairingRole:             normalizePairingRole(cfg.TUIApprovalRole),
		tokenInput:              tokenInput,
		objectiveWorkspaceInput: objectiveWorkspaceInput,
		taskWorkspaceInput:      taskWorkspaceInput,
		objectivesTable:         objectivesTable,
		tasksTable:              tasksTable,
		inspectorViewport:       inspectorVP,
		activityViewport:        activityVP,
		activity:                make([]activityEvent, 0, 256),
	}
	m.applyVisualStyles()
	m.addActivity("info", "session started")
	if strings.TrimSpace(startupInfo) != "" {
		m.addActivity("warn", "startup: "+startupInfo)
	}
	return m
}

func (m model) Init() tea.Cmd {
	return batchCmds(tickCmd(), pollCmd(), bootstrapCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.help.SetWidth(maxInt(20, typed.Width-4))
		m.resizeWidgets()
		return m.finalize(nil)
	case tickMsg:
		m.clock = typed.at.UTC()
		m.frame++
		if m.frame > 1000000 {
			m.frame = 0
		}
		return m.finalize(tickCmd())
	case pollMsg:
		if !m.busy() {
			cmd := m.refreshForPollCmd()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		cmds = append(cmds, pollCmd())
		return m.finalize(batchCmds(cmds...))
	case bootstrapMsg:
		cmd := m.refreshViewAndOverviewCmd("initial load", true)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.statusText = "initial load queued"
		return m.finalize(batchCmds(cmds...))
	case workspaceDebounceMsg:
		if typed.seq != m.debounceSequence {
			return m.finalize(nil)
		}
		trimmed := strings.TrimSpace(typed.value)
		if trimmed == "" || m.busy() {
			return m.finalize(nil)
		}
		switch typed.target {
		case viewObjectives:
			if trimmed != strings.TrimSpace(m.objectiveWorkspaceInput.Value()) {
				return m.finalize(nil)
			}
			cmd := m.beginLoad(1, "loading objectives...")
			cmds = append(cmds, cmd, m.listObjectivesCmd(trimmed, "workspace-change"))
			m.addActivity("info", "workspace changed for objectives: "+trimmed)
		case viewTasks:
			if trimmed != strings.TrimSpace(m.taskWorkspaceInput.Value()) {
				return m.finalize(nil)
			}
			cmd := m.beginLoad(1, "loading tasks...")
			cmds = append(cmds, cmd, m.listTasksCmd(trimmed, m.taskStatusFilter, "workspace-change"))
			m.addActivity("info", "workspace changed for tasks: "+trimmed)
		}
		return m.finalize(batchCmds(cmds...))
	case spinner.TickMsg:
		if !m.busy() {
			return m.finalize(nil)
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m.finalize(cmd)
	case lookupPairingDoneMsg:
		m.endLoad()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.activePair = nil
			m.approvedMsg = nil
			m.addActivity("error", "pairing lookup failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		m.errorText = ""
		m.statusText = "token loaded"
		m.activePair = &typed.pairing
		m.approvedMsg = nil
		m.addActivity("info", "pairing token loaded")
		return m.finalize(nil)
	case approvePairingDoneMsg:
		m.endMutation()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "pairing approval failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		m.errorText = ""
		m.statusText = "pairing approved"
		m.approvedMsg = &typed.response
		m.addActivity("info", "pairing approved for user "+typed.response.ApprovedUserID)
		return m.finalize(nil)
	case denyPairingDoneMsg:
		m.endMutation()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "pairing deny failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		m.errorText = ""
		m.statusText = "pairing denied"
		m.activePair = &typed.pairing
		m.approvedMsg = nil
		m.addActivity("warn", "pairing denied")
		return m.finalize(nil)
	case objectivesLoadedMsg:
		m.endLoad()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "objective load failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		if typed.workspaceID == strings.TrimSpace(m.objectiveWorkspaceInput.Value()) {
			m.objectives = typed.items
			m.rebuildObjectiveRows()
		}
		m.recomputeDashboardStats()
		m.dashboard.LastRefresh = m.clock
		m.statusText = fmt.Sprintf("loaded %d objective(s)", len(typed.items))
		m.errorText = ""
		m.addActivity("info", fmt.Sprintf("loaded %d objectives (%s)", len(typed.items), typed.workspaceID))
		return m.finalize(nil)
	case objectiveActiveDoneMsg:
		m.endMutation()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "objective state update failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		for i := range m.objectives {
			if m.objectives[i].ID == typed.item.ID {
				m.objectives[i] = typed.item
				break
			}
		}
		if typed.item.Active {
			m.statusText = "objective resumed"
			m.addActivity("info", "objective resumed: "+typed.item.ID)
		} else {
			m.statusText = "objective paused"
			m.addActivity("warn", "objective paused: "+typed.item.ID)
		}
		m.errorText = ""
		m.rebuildObjectiveRows()
		m.recomputeDashboardStats()
		return m.finalize(nil)
	case objectiveDeleteDoneMsg:
		m.endMutation()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "objective delete failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		filtered := make([]adminclient.Objective, 0, len(m.objectives))
		for _, item := range m.objectives {
			if item.ID == typed.id {
				continue
			}
			filtered = append(filtered, item)
		}
		m.objectives = filtered
		m.rebuildObjectiveRows()
		m.recomputeDashboardStats()
		m.statusText = "objective deleted"
		m.errorText = ""
		m.addActivity("warn", "objective deleted: "+typed.id)
		return m.finalize(nil)
	case tasksLoadedMsg:
		m.endLoad()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "task load failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		if typed.workspaceID == strings.TrimSpace(m.taskWorkspaceInput.Value()) && typed.status == m.taskStatusFilter {
			m.tasks = typed.items
			m.rebuildTaskRows()
		}
		m.recomputeDashboardStats()
		m.dashboard.LastRefresh = m.clock
		m.statusText = fmt.Sprintf("loaded %d task(s)", len(typed.items))
		m.errorText = ""
		m.addActivity("info", fmt.Sprintf("loaded %d tasks (%s)", len(typed.items), typed.workspaceID))
		return m.finalize(nil)
	case taskRetryDoneMsg:
		m.endMutation()
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.addActivity("error", "task retry failed: "+typed.err.Error())
			return m.finalize(nil)
		}
		m.taskRetryMsg = &typed.response
		m.statusText = "task retried"
		m.errorText = ""
		m.addActivity("info", "task retried: "+typed.response.TaskID)
		workspaceID := strings.TrimSpace(m.taskWorkspaceInput.Value())
		if workspaceID != "" {
			cmd := m.beginLoad(1, "loading tasks...")
			cmds = append(cmds, cmd, m.listTasksCmd(workspaceID, m.taskStatusFilter, "post-retry"))
		}
		return m.finalize(batchCmds(cmds...))
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m.finalize(nil)
	}

	switch {
	case key.Matches(keyMsg, m.keys.Quit):
		m.quitting = true
		return m.finalize(tea.Quit)
	case key.Matches(keyMsg, m.keys.ToggleHelp):
		m.showFullHelp = !m.showFullHelp
		m.help.ShowAll = m.showFullHelp
		if m.showFullHelp {
			m.focus = focusHelp
		} else if m.focus == focusHelp {
			m.focus = focusSidebar
		}
		cmds = append(cmds, m.applyFocusCmd())
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.FocusNext):
		m.focus = nextFocusZone(m.focus)
		cmds = append(cmds, m.applyFocusCmd())
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.FocusPrev):
		m.focus = previousFocusZone(m.focus)
		cmds = append(cmds, m.applyFocusCmd())
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.View1):
		cmds = append(cmds, m.activateView(viewOverview))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.View2):
		cmds = append(cmds, m.activateView(viewPairings))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.View3):
		cmds = append(cmds, m.activateView(viewObjectives))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.View4):
		cmds = append(cmds, m.activateView(viewTasks))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.View5):
		cmds = append(cmds, m.activateView(viewActivity))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.Refresh):
		if !m.busy() {
			cmd := m.refreshViewAndOverviewCmd("manual refresh", true)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m.finalize(batchCmds(cmds...))
	}

	switch m.focus {
	case focusSidebar:
		return m.updateSidebarKey(keyMsg)
	case focusInspector:
		return m.updateInspectorKey(keyMsg)
	case focusHelp:
		if key.Matches(keyMsg, m.keys.Activate) {
			m.showFullHelp = false
			m.help.ShowAll = false
			m.focus = focusSidebar
			cmds = append(cmds, m.applyFocusCmd())
		}
		return m.finalize(batchCmds(cmds...))
	default:
		return m.updateWorkbenchKey(keyMsg)
	}
}

func (m model) View() tea.View {
	v := tea.NewView(m.renderView())
	v.AltScreen = true
	return v
}

func (m model) finalize(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.help.ShowAll = m.showFullHelp
	m.resizeWidgets()
	m.recomputeDashboardStats()
	m.syncInspectorContent()
	m.syncActivityViewport()
	return m, cmd
}

func (m *model) activateView(view viewID) tea.Cmd {
	m.activeView = view
	m.sidebarIndex = sidebarIndexForView(view)
	m.statusText = strings.ToLower(string(view)) + " view"
	m.errorText = ""
	m.addActivity("info", "switched to "+string(view))
	refresh := m.refreshViewAndOverviewCmd("view switch", false)
	return batchCmds(m.applyFocusCmd(), refresh)
}

func (m model) updateSidebarKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(keyMsg, m.keys.Up) {
		if m.sidebarIndex > 0 {
			m.sidebarIndex--
		}
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.Down) {
		if m.sidebarIndex < len(allViews())-1 {
			m.sidebarIndex++
		}
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.Activate) {
		cmd := m.activateView(allViews()[m.sidebarIndex])
		return m.finalize(cmd)
	}
	return m.finalize(nil)
}

func (m model) updateInspectorKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.inspectorViewport, cmd = m.inspectorViewport.Update(keyMsg)
	return m.finalize(cmd)
}

func (m model) updateWorkbenchKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.activeView {
	case viewPairings:
		return m.updatePairingsWorkbenchKey(keyMsg)
	case viewObjectives:
		return m.updateObjectivesWorkbenchKey(keyMsg)
	case viewTasks:
		return m.updateTasksWorkbenchKey(keyMsg)
	case viewActivity:
		var cmd tea.Cmd
		m.activityViewport, cmd = m.activityViewport.Update(keyMsg)
		return m.finalize(cmd)
	default:
		if key.Matches(keyMsg, m.keys.Up) {
			m.focus = focusSidebar
			return m.finalize(m.applyFocusCmd())
		}
		return m.finalize(nil)
	}
}

func (m model) updatePairingsWorkbenchKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch {
	case key.Matches(keyMsg, m.keys.PairRolePrev):
		m.pairingRole = previousPairingRole(m.currentPairingRole())
		m.statusText = "approval role: " + m.pairingRole
		m.errorText = ""
		m.addActivity("info", "pairing role set to "+m.pairingRole)
		return m.finalize(nil)
	case key.Matches(keyMsg, m.keys.PairRoleNext):
		m.pairingRole = nextPairingRole(m.currentPairingRole())
		m.statusText = "approval role: " + m.pairingRole
		m.errorText = ""
		m.addActivity("info", "pairing role set to "+m.pairingRole)
		return m.finalize(nil)
	case key.Matches(keyMsg, m.keys.PairingNew):
		m.activePair = nil
		m.approvedMsg = nil
		m.tokenInput.SetValue("")
		m.statusText = "new token"
		m.errorText = ""
		m.addActivity("info", "ready for new pairing token")
		return m.finalize(nil)
	case key.Matches(keyMsg, m.keys.Activate):
		if m.activePair != nil {
			return m.finalize(nil)
		}
		token := normalizePairingToken(m.tokenInput.Value())
		if token == "" || m.busy() {
			return m.finalize(nil)
		}
		m.tokenInput.SetValue(token)
		cmds = append(cmds, m.beginLoad(1, "loading token..."), m.lookupPairingCmd(token))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.PairApprove):
		if m.activePair == nil || m.busy() {
			return m.finalize(nil)
		}
		token := normalizePairingToken(m.tokenInput.Value())
		if token == "" {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginMutation(1, "approving pairing..."), m.approvePairingCmd(token))
		return m.finalize(batchCmds(cmds...))
	case key.Matches(keyMsg, m.keys.PairDeny):
		if m.activePair == nil || m.busy() {
			return m.finalize(nil)
		}
		token := normalizePairingToken(m.tokenInput.Value())
		if token == "" {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginMutation(1, "denying pairing..."), m.denyPairingCmd(token))
		return m.finalize(batchCmds(cmds...))
	}

	var cmd tea.Cmd
	m.tokenInput, cmd = m.tokenInput.Update(keyMsg)
	m.tokenInput.SetValue(normalizePairingToken(m.tokenInput.Value()))
	return m.finalize(cmd)
}

func (m model) updateObjectivesWorkbenchKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if key.Matches(keyMsg, m.keys.Up) {
		m.objectivesTable.MoveUp(1)
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.Down) {
		m.objectivesTable.MoveDown(1)
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.Activate) {
		workspaceID := strings.TrimSpace(m.objectiveWorkspaceInput.Value())
		if workspaceID == "" || m.busy() {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginLoad(1, "loading objectives..."), m.listObjectivesCmd(workspaceID, "manual"))
		return m.finalize(batchCmds(cmds...))
	}
	if key.Matches(keyMsg, m.keys.ObjectiveToggle) {
		selected, ok := m.selectedObjective()
		if !ok || m.busy() {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginMutation(1, "updating objective state..."), m.setObjectiveActiveCmd(selected.ID, !selected.Active))
		return m.finalize(batchCmds(cmds...))
	}
	if key.Matches(keyMsg, m.keys.ObjectiveDelete) {
		selected, ok := m.selectedObjective()
		if !ok || m.busy() {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginMutation(1, "deleting objective..."), m.deleteObjectiveCmd(selected.ID))
		return m.finalize(batchCmds(cmds...))
	}

	before := m.objectiveWorkspaceInput.Value()
	var cmd tea.Cmd
	m.objectiveWorkspaceInput, cmd = m.objectiveWorkspaceInput.Update(keyMsg)
	m.objectiveWorkspaceInput.SetValue(sanitizeWorkspaceID(m.objectiveWorkspaceInput.Value()))
	if m.objectiveWorkspaceInput.Value() != before {
		m.debounceSequence++
		cmds = append(cmds, cmd, workspaceDebounceCmd(m.debounceSequence, viewObjectives, m.objectiveWorkspaceInput.Value()))
		return m.finalize(batchCmds(cmds...))
	}
	return m.finalize(cmd)
}

func (m model) updateTasksWorkbenchKey(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if key.Matches(keyMsg, m.keys.Up) {
		m.tasksTable.MoveUp(1)
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.Down) {
		m.tasksTable.MoveDown(1)
		return m.finalize(nil)
	}
	if key.Matches(keyMsg, m.keys.TaskFilterPrev) {
		if m.busy() {
			return m.finalize(nil)
		}
		m.taskStatusFilter = previousTaskFilter(m.taskStatusFilter)
		workspaceID := strings.TrimSpace(m.taskWorkspaceInput.Value())
		if workspaceID == "" {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginLoad(1, "loading tasks..."), m.listTasksCmd(workspaceID, m.taskStatusFilter, "filter"))
		m.addActivity("info", "task filter set to "+taskFilterLabel(m.taskStatusFilter))
		return m.finalize(batchCmds(cmds...))
	}
	if key.Matches(keyMsg, m.keys.TaskFilterNext) {
		if m.busy() {
			return m.finalize(nil)
		}
		m.taskStatusFilter = nextTaskFilter(m.taskStatusFilter)
		workspaceID := strings.TrimSpace(m.taskWorkspaceInput.Value())
		if workspaceID == "" {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginLoad(1, "loading tasks..."), m.listTasksCmd(workspaceID, m.taskStatusFilter, "filter"))
		m.addActivity("info", "task filter set to "+taskFilterLabel(m.taskStatusFilter))
		return m.finalize(batchCmds(cmds...))
	}
	if key.Matches(keyMsg, m.keys.TaskRetry) {
		selected, ok := m.selectedTask()
		if !ok || m.busy() {
			return m.finalize(nil)
		}
		if strings.ToLower(strings.TrimSpace(selected.Status)) != "failed" {
			m.errorText = "only failed tasks can be retried"
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginMutation(1, "retrying task..."), m.retryTaskCmd(selected.ID))
		return m.finalize(batchCmds(cmds...))
	}
	if key.Matches(keyMsg, m.keys.Activate) {
		workspaceID := strings.TrimSpace(m.taskWorkspaceInput.Value())
		if workspaceID == "" || m.busy() {
			return m.finalize(nil)
		}
		cmds = append(cmds, m.beginLoad(1, "loading tasks..."), m.listTasksCmd(workspaceID, m.taskStatusFilter, "manual"))
		return m.finalize(batchCmds(cmds...))
	}

	before := m.taskWorkspaceInput.Value()
	var cmd tea.Cmd
	m.taskWorkspaceInput, cmd = m.taskWorkspaceInput.Update(keyMsg)
	m.taskWorkspaceInput.SetValue(sanitizeWorkspaceID(m.taskWorkspaceInput.Value()))
	if m.taskWorkspaceInput.Value() != before {
		m.debounceSequence++
		cmds = append(cmds, cmd, workspaceDebounceCmd(m.debounceSequence, viewTasks, m.taskWorkspaceInput.Value()))
		return m.finalize(batchCmds(cmds...))
	}
	return m.finalize(cmd)
}

func (m *model) refreshForPollCmd() tea.Cmd {
	if m.pendingMutations > 0 {
		return nil
	}
	return m.refreshViewAndOverviewCmd("auto refresh", false)
}

func (m *model) refreshViewAndOverviewCmd(reason string, trackActivity bool) tea.Cmd {
	if m.busy() {
		return nil
	}

	type request struct {
		kind string
		key  string
		cmd  tea.Cmd
	}

	seen := map[string]struct{}{}
	requests := make([]request, 0, 4)

	addLoad := func(kind, dedupeKey string, cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		if _, ok := seen[dedupeKey]; ok {
			return
		}
		seen[dedupeKey] = struct{}{}
		requests = append(requests, request{kind: kind, key: dedupeKey, cmd: cmd})
	}

	objectiveWS := strings.TrimSpace(m.objectiveWorkspaceInput.Value())
	taskWS := strings.TrimSpace(m.taskWorkspaceInput.Value())

	if objectiveWS != "" {
		addLoad("load", "objectives:"+objectiveWS, m.listObjectivesCmd(objectiveWS, "overview"))
	}
	if taskWS != "" {
		addLoad("load", "tasks:"+taskWS+":"+m.taskStatusFilter, m.listTasksCmd(taskWS, m.taskStatusFilter, "overview"))
	}

	switch m.activeView {
	case viewObjectives:
		if objectiveWS != "" {
			addLoad("load", "objectives:"+objectiveWS+":active", m.listObjectivesCmd(objectiveWS, "active-view"))
		}
	case viewTasks:
		if taskWS != "" {
			addLoad("load", "tasks:"+taskWS+":"+m.taskStatusFilter+":active", m.listTasksCmd(taskWS, m.taskStatusFilter, "active-view"))
		}
	}

	if len(requests) == 0 {
		return nil
	}

	cmds := make([]tea.Cmd, 0, len(requests)+1)
	cmds = append(cmds, m.beginLoad(len(requests), "refreshing control plane..."))
	for _, req := range requests {
		cmds = append(cmds, req.cmd)
	}
	if trackActivity {
		m.addActivity("info", reason)
	}
	return batchCmds(cmds...)
}

func (m *model) beginLoad(count int, status string) tea.Cmd {
	if count <= 0 {
		return nil
	}
	wasBusy := m.busy()
	m.pendingLoads += count
	m.statusText = status
	m.errorText = ""
	if !wasBusy {
		return m.spinner.Tick
	}
	return nil
}

func (m *model) beginMutation(count int, status string) tea.Cmd {
	if count <= 0 {
		return nil
	}
	wasBusy := m.busy()
	m.pendingMutations += count
	m.statusText = status
	m.errorText = ""
	if !wasBusy {
		return m.spinner.Tick
	}
	return nil
}

func (m *model) endLoad() {
	if m.pendingLoads > 0 {
		m.pendingLoads--
	}
}

func (m *model) endMutation() {
	if m.pendingMutations > 0 {
		m.pendingMutations--
	}
}

func (m model) busy() bool {
	return m.pendingLoads > 0 || m.pendingMutations > 0
}

func (m *model) applyFocusCmd() tea.Cmd {
	m.objectivesTable.Blur()
	m.tasksTable.Blur()
	m.tokenInput.Blur()
	m.objectiveWorkspaceInput.Blur()
	m.taskWorkspaceInput.Blur()

	cmds := make([]tea.Cmd, 0, 3)

	if m.focus == focusWorkbench {
		switch m.activeView {
		case viewPairings:
			cmds = append(cmds, m.tokenInput.Focus())
		case viewObjectives:
			m.objectivesTable.Focus()
			cmds = append(cmds, m.objectiveWorkspaceInput.Focus())
		case viewTasks:
			m.tasksTable.Focus()
			cmds = append(cmds, m.taskWorkspaceInput.Focus())
		}
	}
	return batchCmds(cmds...)
}

func (m *model) applyVisualStyles() {
	t := newTheme()

	m.spinner.Style = t.spinner

	inputStyles := textinput.DefaultDarkStyles()
	inputStyles.Focused.Prompt = t.inputPrompt
	inputStyles.Focused.Text = t.inputText
	inputStyles.Focused.Placeholder = t.inputPlaceholder
	inputStyles.Blurred.Prompt = t.inputPrompt
	inputStyles.Blurred.Text = t.inputText
	inputStyles.Blurred.Placeholder = t.inputPlaceholder
	m.tokenInput.SetStyles(inputStyles)
	m.objectiveWorkspaceInput.SetStyles(inputStyles)
	m.taskWorkspaceInput.SetStyles(inputStyles)

	tableStyles := table.DefaultStyles()
	tableStyles.Header = t.tableHeader
	tableStyles.Cell = t.tableCell
	tableStyles.Selected = t.tableSelected
	m.objectivesTable.SetStyles(tableStyles)
	m.tasksTable.SetStyles(tableStyles)

	m.help.Styles.Ellipsis = t.footerInfo
	m.help.Styles.ShortKey = t.footerKey
	m.help.Styles.ShortDesc = t.footerInfo
	m.help.Styles.ShortSeparator = t.panelSubtle
	m.help.Styles.FullKey = t.footerKey
	m.help.Styles.FullDesc = t.footerInfo
	m.help.Styles.FullSeparator = t.panelSubtle
}

func (m *model) resizeWidgets() {
	layout := computeLayout(m.width, m.height)

	mainWidth := maxInt(20, layout.MainWidth-2)
	mainHeight := maxInt(7, layout.BodyHeight-2)
	inspectorWidth := maxInt(20, layout.InspectorWidth-2)
	inspectorHeight := maxInt(6, layout.BodyHeight-2)

	if layout.Compact {
		mainWidth = maxInt(20, layout.Width-4)
		mainHeight = maxInt(5, layout.CompactMainHeight-2)
		inspectorWidth = maxInt(20, layout.Width-4)
		inspectorHeight = maxInt(6, layout.CompactInspectorHeight-2)
	}

	m.tokenInput.SetWidth(maxInt(12, mainWidth-10))
	m.objectiveWorkspaceInput.SetWidth(maxInt(8, mainWidth-14))
	m.taskWorkspaceInput.SetWidth(maxInt(8, mainWidth-14))

	m.setObjectiveColumns(mainWidth)
	m.setTaskColumns(mainWidth)
	m.objectivesTable.SetWidth(mainWidth)
	m.tasksTable.SetWidth(mainWidth)
	m.objectivesTable.SetHeight(mainHeight)
	m.tasksTable.SetHeight(mainHeight)

	m.inspectorViewport.SetWidth(maxInt(16, inspectorWidth))
	m.inspectorViewport.SetHeight(maxInt(4, inspectorHeight))

	m.activityViewport.SetWidth(maxInt(16, mainWidth))
	m.activityViewport.SetHeight(maxInt(4, mainHeight))
}

func (m *model) setObjectiveColumns(mainWidth int) {
	// Table cells include horizontal padding from styles; reserve that space
	// so rendered rows never overflow the table viewport width.
	usable := maxInt(24, mainWidth-8) // 4 columns * 2 padding
	stateWidth := 9
	triggerWidth := 10
	nextWidth := 18
	titleWidth := usable - stateWidth - triggerWidth - nextWidth

	if titleWidth < 12 {
		nextWidth = maxInt(10, usable-stateWidth-triggerWidth-12)
		titleWidth = usable - stateWidth - triggerWidth - nextWidth
	}
	if titleWidth < 8 {
		titleWidth = 8
	}
	nextWidth = maxInt(10, usable-titleWidth-stateWidth-triggerWidth)

	columns := []table.Column{
		{Title: "Title", Width: titleWidth},
		{Title: "State", Width: stateWidth},
		{Title: "Trigger", Width: triggerWidth},
		{Title: "Next Run", Width: nextWidth},
	}
	m.objectivesTable.SetColumns(columns)
}

func (m *model) setTaskColumns(mainWidth int) {
	usable := maxInt(28, mainWidth-10) // 5 columns * 2 padding
	statusWidth := 9
	kindWidth := 10
	tryWidth := 3
	updatedWidth := 18
	titleWidth := usable - statusWidth - kindWidth - tryWidth - updatedWidth

	if titleWidth < 12 {
		updatedWidth = maxInt(10, usable-statusWidth-kindWidth-tryWidth-12)
		titleWidth = usable - statusWidth - kindWidth - tryWidth - updatedWidth
	}
	if titleWidth < 8 {
		titleWidth = 8
	}
	updatedWidth = maxInt(10, usable-titleWidth-statusWidth-kindWidth-tryWidth)

	columns := []table.Column{
		{Title: "Title", Width: titleWidth},
		{Title: "Status", Width: statusWidth},
		{Title: "Kind", Width: kindWidth},
		{Title: "Try", Width: tryWidth},
		{Title: "Updated", Width: updatedWidth},
	}
	m.tasksTable.SetColumns(columns)
}

func (m *model) rebuildObjectiveRows() {
	rows := make([]table.Row, 0, len(m.objectives))
	for _, item := range m.objectives {
		state := "paused"
		if item.Active {
			state = "active"
		}
		nextRun := formatUnixPtr(item.NextRunUnix)
		rows = append(rows, table.Row{item.Title, state, item.TriggerType, nextRun})
	}
	cursor := m.objectivesTable.Cursor()
	m.objectivesTable.SetRows(rows)
	if len(rows) == 0 {
		m.objectivesTable.SetCursor(0)
		return
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	m.objectivesTable.SetCursor(cursor)
}

func (m *model) rebuildTaskRows() {
	rows := make([]table.Row, 0, len(m.tasks))
	for _, item := range m.tasks {
		rows = append(rows, table.Row{
			item.Title,
			strings.ToLower(strings.TrimSpace(item.Status)),
			item.Kind,
			strconv.Itoa(item.Attempts),
			formatUnix(item.UpdatedAtUnix),
		})
	}
	cursor := m.tasksTable.Cursor()
	m.tasksTable.SetRows(rows)
	if len(rows) == 0 {
		m.tasksTable.SetCursor(0)
		return
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	m.tasksTable.SetCursor(cursor)
}

func (m *model) recomputeDashboardStats() {
	stats := dashboardStats{}

	stats.ObjectivesTotal = len(m.objectives)
	for _, item := range m.objectives {
		if item.Active {
			stats.ObjectivesActive++
		}
		if strings.TrimSpace(item.LastError) != "" {
			stats.ObjectivesFailed++
		}
	}

	stats.TasksTotal = len(m.tasks)
	for _, item := range m.tasks {
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "queued":
			stats.TasksQueued++
		case "running":
			stats.TasksRunning++
		case "failed":
			stats.TasksFailed++
		case "succeeded":
			stats.TasksSucceeded++
		}
	}
	if !m.dashboard.LastRefresh.IsZero() {
		stats.LastRefresh = m.dashboard.LastRefresh
	}
	m.dashboard = stats
}

func (m *model) syncInspectorContent() {
	content := ""
	switch m.activeView {
	case viewPairings:
		content = m.renderPairingsInspectorText()
	case viewObjectives:
		content = m.renderObjectivesInspectorText()
	case viewTasks:
		content = m.renderTasksInspectorText()
	case viewActivity:
		content = m.renderActivityInspectorText()
	default:
		content = m.renderOverviewInspectorText()
	}
	m.inspectorViewport.SetContent(content)
}

func (m *model) syncActivityViewport() {
	t := newTheme()

	lines := make([]string, 0, len(m.activity))
	for index := len(m.activity) - 1; index >= 0; index-- {
		item := m.activity[index]
		level := strings.ToUpper(item.Level)
		levelStyle := t.panelSubtle
		switch strings.ToLower(item.Level) {
		case "error":
			levelStyle = t.panelError
		case "warn":
			levelStyle = t.panelWarn
		case "info":
			levelStyle = t.panelSuccess
		}
		lines = append(lines, fmt.Sprintf("%s  [%s]  %s", t.panelSubtle.Render(item.At.UTC().Format(time.RFC3339)), levelStyle.Render(level), item.Message))
	}
	if len(lines) == 0 {
		lines = append(lines, "No activity in this session yet.")
	}
	m.activityViewport.SetContent(strings.Join(lines, "\n"))
}

func (m *model) addActivity(level, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	m.activity = append(m.activity, activityEvent{
		At:      m.clock,
		Level:   strings.ToLower(strings.TrimSpace(level)),
		Message: strings.TrimSpace(message),
	})
	if len(m.activity) > 250 {
		m.activity = m.activity[len(m.activity)-250:]
	}
}

func (m model) selectedObjective() (adminclient.Objective, bool) {
	cursor := m.objectivesTable.Cursor()
	if cursor < 0 || cursor >= len(m.objectives) {
		return adminclient.Objective{}, false
	}
	return m.objectives[cursor], true
}

func (m model) selectedTask() (adminclient.Task, bool) {
	cursor := m.tasksTable.Cursor()
	if cursor < 0 || cursor >= len(m.tasks) {
		return adminclient.Task{}, false
	}
	return m.tasks[cursor], true
}

func (m model) currentPairingRole() string {
	return normalizePairingRole(m.pairingRole)
}

type lookupPairingDoneMsg struct {
	pairing adminclient.Pairing
	err     error
}

type approvePairingDoneMsg struct {
	response adminclient.ApprovePairingResponse
	err      error
}

type denyPairingDoneMsg struct {
	pairing adminclient.Pairing
	err     error
}

type objectivesLoadedMsg struct {
	items       []adminclient.Objective
	workspaceID string
	source      string
	err         error
}

type objectiveActiveDoneMsg struct {
	item adminclient.Objective
	err  error
}

type objectiveDeleteDoneMsg struct {
	id  string
	err error
}

type tasksLoadedMsg struct {
	items       []adminclient.Task
	workspaceID string
	status      string
	source      string
	err         error
}

type taskRetryDoneMsg struct {
	response adminclient.RetryTaskResponse
	err      error
}

func (m model) lookupPairingCmd(token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pairing, err := m.client.LookupPairing(ctx, token)
		return lookupPairingDoneMsg{pairing: pairing, err: err}
	}
}

func (m model) approvePairingCmd(token string) tea.Cmd {
	role := m.currentPairingRole()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		response, err := m.client.ApprovePairing(ctx, token, m.cfg.TUIApproverUserID, role, "")
		return approvePairingDoneMsg{response: response, err: err}
	}
}

func (m model) denyPairingCmd(token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pairing, err := m.client.DenyPairing(ctx, token, m.cfg.TUIApproverUserID, "denied by admin")
		return denyPairingDoneMsg{pairing: pairing, err: err}
	}
}

func (m model) listObjectivesCmd(workspaceID, source string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		items, err := m.client.ListObjectives(ctx, workspaceID, false, 100)
		return objectivesLoadedMsg{items: items, workspaceID: workspaceID, source: source, err: err}
	}
}

func (m model) setObjectiveActiveCmd(objectiveID string, active bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		item, err := m.client.SetObjectiveActive(ctx, objectiveID, active)
		return objectiveActiveDoneMsg{item: item, err: err}
	}
}

func (m model) deleteObjectiveCmd(objectiveID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		err := m.client.DeleteObjective(ctx, objectiveID)
		return objectiveDeleteDoneMsg{id: objectiveID, err: err}
	}
}

func (m model) listTasksCmd(workspaceID, status, source string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		items, err := m.client.ListTasks(ctx, workspaceID, status, 200)
		return tasksLoadedMsg{items: items, workspaceID: workspaceID, status: status, source: source, err: err}
	}
}

func (m model) retryTaskCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		response, err := m.client.RetryTask(ctx, taskID)
		return taskRetryDoneMsg{response: response, err: err}
	}
}

var taskFilterCycle = []string{"", "failed", "queued", "running", "succeeded"}

func nextTaskFilter(current string) string {
	value := strings.ToLower(strings.TrimSpace(current))
	for index, item := range taskFilterCycle {
		if value == item {
			return taskFilterCycle[(index+1)%len(taskFilterCycle)]
		}
	}
	return taskFilterCycle[0]
}

func previousTaskFilter(current string) string {
	value := strings.ToLower(strings.TrimSpace(current))
	for index, item := range taskFilterCycle {
		if value == item {
			if index == 0 {
				return taskFilterCycle[len(taskFilterCycle)-1]
			}
			return taskFilterCycle[index-1]
		}
	}
	return taskFilterCycle[0]
}

func taskFilterLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "failed":
		return "failed"
	case "queued":
		return "queued"
	case "running":
		return "running"
	case "succeeded":
		return "succeeded"
	default:
		return "all"
	}
}

var pairingRoleCycle = []string{"overlord", "admin", "operator", "member", "viewer"}

func normalizePairingRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "overlord":
		return "overlord"
	case "admin":
		return "admin"
	case "operator":
		return "operator"
	case "member":
		return "member"
	case "viewer":
		return "viewer"
	default:
		return "admin"
	}
}

func nextPairingRole(current string) string {
	role := normalizePairingRole(current)
	for index, item := range pairingRoleCycle {
		if item == role {
			return pairingRoleCycle[(index+1)%len(pairingRoleCycle)]
		}
	}
	return pairingRoleCycle[0]
}

func previousPairingRole(current string) string {
	role := normalizePairingRole(current)
	for index, item := range pairingRoleCycle {
		if item != role {
			continue
		}
		if index == 0 {
			return pairingRoleCycle[len(pairingRoleCycle)-1]
		}
		return pairingRoleCycle[index-1]
	}
	return pairingRoleCycle[0]
}

func normalizePairingToken(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	builder := strings.Builder{}
	for _, ch := range value {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func sanitizeWorkspaceID(value string) string {
	value = strings.TrimSpace(value)
	builder := strings.Builder{}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		filtered = append(filtered, cmd)
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

func allViews() []viewID {
	return []viewID{viewOverview, viewPairings, viewObjectives, viewTasks, viewActivity}
}

func viewLabel(view viewID) string {
	switch view {
	case viewOverview:
		return "Overview"
	case viewPairings:
		return "Pairings"
	case viewObjectives:
		return "Objectives"
	case viewTasks:
		return "Tasks"
	case viewActivity:
		return "Activity"
	default:
		return strings.Title(string(view))
	}
}

func sidebarIndexForView(view viewID) int {
	views := allViews()
	for index, item := range views {
		if item == view {
			return index
		}
	}
	return 0
}

func nextFocusZone(current focusZone) focusZone {
	switch current {
	case focusSidebar:
		return focusWorkbench
	case focusWorkbench:
		return focusInspector
	case focusInspector:
		return focusHelp
	default:
		return focusSidebar
	}
}

func previousFocusZone(current focusZone) focusZone {
	switch current {
	case focusSidebar:
		return focusHelp
	case focusHelp:
		return focusInspector
	case focusInspector:
		return focusWorkbench
	default:
		return focusSidebar
	}
}

func focusLabel(zone focusZone) string {
	switch zone {
	case focusSidebar:
		return "sidebar"
	case focusWorkbench:
		return "workbench"
	case focusInspector:
		return "inspector"
	case focusHelp:
		return "help"
	default:
		return "unknown"
	}
}

func formatUnix(value int64) string {
	if value <= 0 {
		return "n/a"
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func formatUnixPtr(value *int64) string {
	if value == nil {
		return "n/a"
	}
	return formatUnix(*value)
}

func humanDurationMs(value int64) string {
	if value <= 0 {
		return "n/a"
	}
	return (time.Duration(value) * time.Millisecond).String()
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func maxInt(first, second int) int {
	if first > second {
		return first
	}
	return second
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func syncEnvAtStartup(cfg config.Config, logger *slog.Logger) (config.Config, string) {
	result, err := envsync.SyncLocalPKIEnv()
	if err != nil {
		logger.Warn("tui env sync failed", "error", err)
		return cfg, "startup env sync failed"
	}

	infoParts := []string{}
	if result.Skipped {
		if strings.TrimSpace(result.Reason) != "" {
			infoParts = append(infoParts, result.Reason)
		}
	} else if len(result.UpdatedKeys) > 0 {
		infoParts = append(infoParts, "synced local mTLS paths to .env")
		logger.Info("tui env sync updated keys", "keys", strings.Join(result.UpdatedKeys, ","))
		if strings.TrimSpace(result.BackupPath) != "" {
			logger.Info("tui env sync created backup", "path", result.BackupPath)
		}
	}

	if cfg.AdminTLSCAFile == "" {
		path := filepathOrEmpty(result.PKIDir, "clients-ca.crt")
		if path != "" {
			cfg.AdminTLSCAFile = path
			_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CA_FILE", path)
		}
	}
	if cfg.AdminTLSCertFile == "" {
		path := filepathOrEmpty(result.PKIDir, "admin-client.crt")
		if path != "" {
			cfg.AdminTLSCertFile = path
			_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CERT_FILE", path)
		}
	}
	if cfg.AdminTLSKeyFile == "" {
		path := filepathOrEmpty(result.PKIDir, "admin-client.key")
		if path != "" {
			cfg.AdminTLSKeyFile = path
			_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_KEY_FILE", path)
		}
	}

	cfg, recoveredInfo := recoverInvalidTLSConfig(cfg, result.PKIDir, logger)
	if strings.TrimSpace(recoveredInfo) != "" {
		infoParts = append(infoParts, recoveredInfo)
	}
	return cfg, strings.Join(infoParts, "; ")
}

func filepathOrEmpty(dir, file string) string {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(file) == "" {
		return ""
	}
	path := filepath.Join(dir, file)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func recoverInvalidTLSConfig(cfg config.Config, pkiDir string, logger *slog.Logger) (config.Config, string) {
	info := ""

	if strings.TrimSpace(cfg.AdminTLSCAFile) != "" && !validCACert(cfg.AdminTLSCAFile) {
		logger.Warn("invalid admin ca file configured, clearing for tui session", "path", cfg.AdminTLSCAFile)
		cfg.AdminTLSCAFile = ""
		_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CA_FILE", "")
		info = "ignored invalid CA path in environment"
	}

	certPath := strings.TrimSpace(cfg.AdminTLSCertFile)
	keyPath := strings.TrimSpace(cfg.AdminTLSKeyFile)
	if certPath == "" && keyPath == "" {
		return cfg, info
	}

	if certPath == "" || keyPath == "" {
		logger.Warn("incomplete admin client cert configuration, clearing for tui session")
		cfg.AdminTLSCertFile = ""
		cfg.AdminTLSKeyFile = ""
		_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CERT_FILE", "")
		_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_KEY_FILE", "")
		if info == "" {
			info = "ignored incomplete client cert config"
		}
		return cfg, info
	}

	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return cfg, info
	}

	fallbackCert := filepathOrEmpty(pkiDir, "admin-client.crt")
	fallbackKey := filepathOrEmpty(pkiDir, "admin-client.key")
	if fallbackCert != "" && fallbackKey != "" {
		if _, err := tls.LoadX509KeyPair(fallbackCert, fallbackKey); err == nil {
			cfg.AdminTLSCertFile = fallbackCert
			cfg.AdminTLSKeyFile = fallbackKey
			_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CERT_FILE", fallbackCert)
			_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_KEY_FILE", fallbackKey)
			if info == "" {
				info = "recovered client cert path from local caddy pki"
			}
			return cfg, info
		}
	}

	logger.Warn("invalid admin client cert configuration, continuing without client cert for tui")
	cfg.AdminTLSCertFile = ""
	cfg.AdminTLSKeyFile = ""
	_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_CERT_FILE", "")
	_ = os.Setenv("AGENT_RUNTIME_ADMIN_TLS_KEY_FILE", "")
	if info == "" {
		info = "ignored invalid client cert config"
	}
	return cfg, info
}

func validCACert(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	return pool.AppendCertsFromPEM(content)
}
