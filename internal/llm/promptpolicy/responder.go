package promptpolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/store"
)

type PolicyProvider interface {
	LookupContextPolicy(ctx context.Context, contextID string) (store.ContextPolicy, error)
}

type Config struct {
	WorkspaceRoot        string
	AdminSystemPrompt    string
	PublicSystemPrompt   string
	GlobalSoulPath       string
	WorkspaceSoulRelPath string
	ContextSoulRelPath   string
	MaxSkills            int
	MaxSkillBytes        int
	MaxSoulBytes         int
	MaxSystemPromptBytes int
}

type Responder struct {
	base     llm.Responder
	provider PolicyProvider
	cfg      Config
}

func New(base llm.Responder, provider PolicyProvider, cfg Config) *Responder {
	if cfg.MaxSkills < 1 {
		cfg.MaxSkills = 5
	}
	if cfg.MaxSkillBytes < 300 {
		cfg.MaxSkillBytes = 1400
	}
	if cfg.MaxSoulBytes < 300 {
		cfg.MaxSoulBytes = 2400
	}
	if cfg.MaxSystemPromptBytes < 800 {
		cfg.MaxSystemPromptBytes = 12000
	}
	return &Responder{
		base:     base,
		provider: provider,
		cfg:      cfg,
	}
}

func (r *Responder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if r.base == nil {
		return "", fmt.Errorf("%w: base responder missing", llm.ErrUnavailable)
	}
	augmented := input
	augmented.SystemPrompt = r.buildSystemPrompt(ctx, input)
	return r.base.Reply(ctx, augmented)
}

func (r *Responder) buildSystemPrompt(ctx context.Context, input llm.MessageInput) string {
	policy := store.ContextPolicy{
		ContextID:   input.ContextID,
		WorkspaceID: input.WorkspaceID,
	}
	if r.provider != nil && strings.TrimSpace(input.ContextID) != "" {
		loaded, err := r.provider.LookupContextPolicy(ctx, input.ContextID)
		if err == nil {
			policy = loaded
		} else if !errors.Is(err, store.ErrContextNotFound) {
			// noop fallback
		}
	}

	lines := []string{}
	if policy.IsAdmin {
		if strings.TrimSpace(r.cfg.AdminSystemPrompt) != "" {
			lines = append(lines, strings.TrimSpace(r.cfg.AdminSystemPrompt))
		}
	} else if strings.TrimSpace(r.cfg.PublicSystemPrompt) != "" {
		lines = append(lines, strings.TrimSpace(r.cfg.PublicSystemPrompt))
	}
	if strings.TrimSpace(policy.SystemPrompt) != "" {
		lines = append(lines, "Context policy:\n"+strings.TrimSpace(policy.SystemPrompt))
	}
	soulSections := r.loadSoulSections(policy.WorkspaceID, policy.ContextID)
	if len(soulSections) > 0 {
		lines = append(lines, "SOUL behavior directives:")
		lines = append(lines, soulSections...)
	}
	lines = append(lines, "External actions policy:\nIf you need to request an external action (email/send/post/run), include an `action` fenced JSON block. Example:\n```action\n{\"type\":\"send_email\",\"target\":\"ops@example.com\",\"summary\":\"Send update\",\"subject\":\"Status\",\"body\":\"...\"}\n```\nFor shell/CLI execution use:\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch service status\",\"args\":[\"-sS\",\"https://example.com/health\"]}\n```\nThese actions require admin approval before execution. Command execution is restricted by sandbox policy allowlists.")

	skills := r.loadSkills(policy.WorkspaceID, policy.ContextID, policy.IsAdmin)
	if len(skills) > 0 {
		lines = append(lines, "Skill templates:")
		for _, skill := range skills {
			lines = append(lines, skill)
		}
	}

	prompt := strings.TrimSpace(strings.Join(lines, "\n\n"))
	if len(prompt) > r.cfg.MaxSystemPromptBytes {
		return prompt[:r.cfg.MaxSystemPromptBytes]
	}
	return prompt
}

func (r *Responder) loadSkills(workspaceID, contextID string, isAdmin bool) []string {
	root := strings.TrimSpace(r.cfg.WorkspaceRoot)
	workspaceID = strings.TrimSpace(workspaceID)
	contextID = strings.TrimSpace(contextID)
	if root == "" || workspaceID == "" {
		return nil
	}
	dirs := []string{
		filepath.Join(root, workspaceID, "skills", "common"),
		filepath.Join(root, workspaceID, "skills", "contexts", contextID),
	}
	if isAdmin {
		dirs = append(dirs, filepath.Join(root, workspaceID, "skills", "admin"))
	} else {
		dirs = append(dirs, filepath.Join(root, workspaceID, "skills", "public"))
	}

	files := make([]string, 0, 16)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
				continue
			}
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	skills := make([]string, 0, r.cfg.MaxSkills)
	for _, path := range files {
		if len(skills) >= r.cfg.MaxSkills {
			break
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(content))
		if text == "" {
			continue
		}
		if len(text) > r.cfg.MaxSkillBytes {
			text = text[:r.cfg.MaxSkillBytes] + "..."
		}
		skills = append(skills, fmt.Sprintf("- `%s`: %s", filepath.Base(path), strings.Join(strings.Fields(text), " ")))
	}
	return skills
}

func (r *Responder) loadSoulSections(workspaceID, contextID string) []string {
	sections := []string{}
	if text, ok := r.readSoulFile(strings.TrimSpace(r.cfg.GlobalSoulPath)); ok {
		sections = append(sections, "Global SOUL:\n"+text)
	}

	workspaceRoot := strings.TrimSpace(r.cfg.WorkspaceRoot)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceRoot == "" || workspaceID == "" {
		return sections
	}

	workspaceRelative := strings.TrimSpace(r.cfg.WorkspaceSoulRelPath)
	if workspaceRelative != "" {
		path := filepath.Join(workspaceRoot, workspaceID, filepath.FromSlash(workspaceRelative))
		if text, ok := r.readSoulFile(path); ok {
			sections = append(sections, "Workspace SOUL override:\n"+text)
		}
	}

	contextRelative := strings.TrimSpace(r.cfg.ContextSoulRelPath)
	contextID = strings.TrimSpace(contextID)
	if contextRelative == "" || contextID == "" {
		return sections
	}
	contextRelative = strings.ReplaceAll(contextRelative, "{context_id}", sanitizeSoulPathSegment(contextID))
	path := filepath.Join(workspaceRoot, workspaceID, filepath.FromSlash(contextRelative))
	if text, ok := r.readSoulFile(path); ok {
		sections = append(sections, "Agent SOUL override:\n"+text)
	}
	return sections
}

func (r *Responder) readSoulFile(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return "", false
	}
	if len(text) > r.cfg.MaxSoulBytes {
		text = text[:r.cfg.MaxSoulBytes] + "..."
	}
	return text, true
}

var soulPathSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeSoulPathSegment(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "default"
	}
	value := soulPathSanitizer.ReplaceAllString(trimmed, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "default"
	}
	return value
}
