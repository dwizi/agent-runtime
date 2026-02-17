package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

func (s *Service) handleRouteOverride(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}
	policy, err := s.store.LookupContextPolicyByExternal(ctx, input.Connector, input.ExternalID)
	if err != nil {
		return MessageOutput{}, err
	}
	if !policy.IsAdmin {
		return MessageOutput{Handled: true, Reply: "Access denied: route overrides are only allowed in admin channels."}, nil
	}

	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		return MessageOutput{Handled: true, Reply: "Usage: /route <task-id> <question|issue|task|moderation|noise> [p1|p2|p3] [due-window like 2h or 1d]"}, nil
	}
	taskID := strings.TrimSpace(fields[0])
	taskRecord, err := s.store.LookupTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return MessageOutput{Handled: true, Reply: "Task not found."}, nil
		}
		return MessageOutput{}, err
	}
	if strings.TrimSpace(taskRecord.WorkspaceID) != "" && strings.TrimSpace(policy.WorkspaceID) != "" &&
		!strings.EqualFold(strings.TrimSpace(taskRecord.WorkspaceID), strings.TrimSpace(policy.WorkspaceID)) {
		return MessageOutput{Handled: true, Reply: "Access denied: task belongs to a different workspace."}, nil
	}
	class, ok := normalizeTriageClass(fields[1])
	if !ok {
		return MessageOutput{Handled: true, Reply: "Invalid route class. Use: question, issue, task, moderation, noise."}, nil
	}
	priority, dueWindow, lane := routingDefaults(class)
	if len(fields) >= 3 {
		overridePriority, priorityOK := normalizeTriagePriority(fields[2])
		if priorityOK {
			priority = overridePriority
		} else {
			parsedWindow, dueErr := parseDueWindow(fields[2])
			if dueErr == nil {
				dueWindow = parsedWindow
			}
		}
	}
	if len(fields) >= 4 {
		parsedWindow, dueErr := parseDueWindow(fields[3])
		if dueErr != nil {
			return MessageOutput{Handled: true, Reply: "Invalid due window. Examples: `2h`, `8h`, `1d`, `2d`."}, nil
		}
		dueWindow = parsedWindow
	}
	if class == TriageNoise {
		priority = TriagePriorityP3
		dueWindow = 0
		lane = "backlog"
	}
	dueAt := time.Time{}
	if dueWindow > 0 {
		dueAt = time.Now().UTC().Add(dueWindow)
	}
	updated, err := s.store.UpdateTaskRouting(ctx, store.UpdateTaskRoutingInput{
		ID:           taskID,
		RouteClass:   string(class),
		Priority:     string(priority),
		DueAt:        dueAt,
		AssignedLane: lane,
	})
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return MessageOutput{Handled: true, Reply: "Task not found."}, nil
		}
		return MessageOutput{}, err
	}
	reply := fmt.Sprintf("Routing updated for `%s`:\n- class: `%s`\n- priority: `%s`\n- lane: `%s`", updated.ID, class, priority, lane)
	if !dueAt.IsZero() {
		reply += fmt.Sprintf("\n- due: `%s`", dueAt.UTC().Format(time.RFC3339))
	} else {
		reply += "\n- due: `(none)`"
	}
	return MessageOutput{Handled: true, Reply: reply}, nil
}

func (s *Service) enqueueAndPersistTask(ctx context.Context, input store.CreateTaskInput) (orchestrator.Task, error) {
	task, err := s.engine.Enqueue(orchestrator.Task{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ContextID:   strings.TrimSpace(input.ContextID),
		Kind:        orchestrator.TaskKind(strings.TrimSpace(input.Kind)),
		Title:       strings.TrimSpace(input.Title),
		Prompt:      strings.TrimSpace(input.Prompt),
	})
	if err != nil {
		return orchestrator.Task{}, err
	}
	input.ID = task.ID
	input.WorkspaceID = task.WorkspaceID
	input.ContextID = task.ContextID
	input.Kind = string(task.Kind)
	input.Title = task.Title
	input.Prompt = task.Prompt
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "queued"
	}
	if err := s.store.CreateTask(ctx, input); err != nil {
		return orchestrator.Task{}, err
	}
	return task, nil
}

func (s *Service) handleAdminChannel(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	if strings.ToLower(strings.TrimSpace(arg)) != "enable" {
		return MessageOutput{Handled: true, Reply: "Usage: /admin-channel enable"}, nil
	}

	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	contextRecord, err := s.store.SetContextAdminByExternal(ctx, input.Connector, input.ExternalID, true)
	if err != nil {
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Admin channel enabled for context `%s`.", contextRecord.ID),
	}, nil
}

func (s *Service) handleApprove(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	token := strings.TrimSpace(arg)
	if token == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /approve <pairing-token>"}, nil
	}
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	result, err := s.store.ApprovePairing(ctx, store.ApprovePairingInput{
		Token:          token,
		ApproverUserID: identity.UserID,
		Role:           identity.Role,
	})
	if err != nil {
		if errors.Is(err, store.ErrPairingNotFound) {
			return MessageOutput{Handled: true, Reply: "Pairing token not found."}, nil
		}
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Pairing approved for `%s` (%s).", result.PairingRequest.DisplayName, result.UserID),
	}, nil
}

func (s *Service) handleDeny(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	token := strings.TrimSpace(arg)
	if token == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /deny <pairing-token> [reason]"}, nil
	}

	parts := strings.Fields(token)
	reason := "denied by admin"
	actualToken := parts[0]
	if len(parts) > 1 {
		reason = strings.Join(parts[1:], " ")
	}

	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	request, err := s.store.DenyPairing(ctx, store.DenyPairingInput{
		Token:          actualToken,
		ApproverUserID: identity.UserID,
		Reason:         reason,
	})
	if err != nil {
		if errors.Is(err, store.ErrPairingNotFound) {
			return MessageOutput{Handled: true, Reply: "Pairing token not found."}, nil
		}
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Pairing denied for `%s`.", request.DisplayName),
	}, nil
}
