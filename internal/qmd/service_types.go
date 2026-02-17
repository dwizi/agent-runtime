package qmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnavailable   = errors.New("qmd is unavailable")
	ErrNotFound      = errors.New("document not found")
	ErrInvalidTarget = errors.New("invalid document target")
)

type Config struct {
	WorkspaceRoot   string
	Binary          string
	SidecarURL      string
	IndexName       string
	Collection      string
	SharedModelsDir string
	EmbedExclude    []string
	SearchLimit     int
	OpenMaxBytes    int
	Debounce        time.Duration
	IndexTimeout    time.Duration
	QueryTimeout    time.Duration
	AutoEmbed       bool
}

type SearchResult struct {
	Path    string
	DocID   string
	Score   float64
	Snippet string
}

type OpenResult struct {
	Path      string
	Content   string
	Truncated bool
}

type Status struct {
	WorkspaceID    string
	WorkspacePath  string
	WorkspaceExist bool
	Pending        bool
	Indexed        bool
	LastIndexedAt  time.Time
	QMDBinary      string
	IndexFile      string
	IndexExists    bool
	Summary        string
}

type Service struct {
	cfg          Config
	logger       *slog.Logger
	runner       commandRunner
	mu           sync.Mutex
	timers       map[string]*time.Timer
	pendingEmbed map[string]bool
	locks        map[string]*sync.Mutex
	collections  map[string]bool
	indexed      map[string]bool
	lastIndexed  map[string]time.Time
	closed       bool
}

type commandRunner interface {
	Run(cmd *exec.Cmd) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

type commandError struct {
	command string
	output  string
	err     error
}

func (e *commandError) Error() string {
	if e.output == "" {
		return fmt.Sprintf("%s: %v", e.command, e.err)
	}
	return fmt.Sprintf("%s: %v: %s", e.command, e.err, e.output)
}

func (e *commandError) Unwrap() error {
	return e.err
}

func New(cfg Config, logger *slog.Logger) *Service {
	return newService(cfg, logger, execRunner{})
}

func newService(cfg Config, logger *slog.Logger, runner commandRunner) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if runner == nil {
		runner = execRunner{}
	}
	cfg = withDefaults(cfg)
	return &Service{
		cfg:          cfg,
		logger:       logger,
		runner:       runner,
		timers:       map[string]*time.Timer{},
		pendingEmbed: map[string]bool{},
		locks:        map[string]*sync.Mutex{},
		collections:  map[string]bool{},
		indexed:      map[string]bool{},
		lastIndexed:  map[string]time.Time{},
	}
}

func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "qmd"
	}
	if strings.TrimSpace(cfg.IndexName) == "" {
		cfg.IndexName = "agent-runtime"
	}
	if strings.TrimSpace(cfg.Collection) == "" {
		cfg.Collection = "workspace"
	}
	if cfg.SearchLimit < 1 {
		cfg.SearchLimit = 5
	}
	if cfg.OpenMaxBytes < 256 {
		cfg.OpenMaxBytes = 1600
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = 3 * time.Second
	}
	if cfg.IndexTimeout <= 0 {
		cfg.IndexTimeout = 3 * time.Minute
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	if len(cfg.EmbedExclude) > 0 {
		normalized := make([]string, 0, len(cfg.EmbedExclude))
		seen := map[string]struct{}{}
		for _, pattern := range cfg.EmbedExclude {
			value := normalizeEmbedPattern(pattern)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			normalized = append(normalized, value)
		}
		cfg.EmbedExclude = normalized
	}
	return cfg
}
