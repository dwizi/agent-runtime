package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type Service struct {
	roots    []string
	logger   *slog.Logger
	onChange func(context.Context, string)
	watcher  *fsnotify.Watcher
}

func New(roots []string, logger *slog.Logger, onChange func(context.Context, string)) (*Service, error) {
	fileWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	return &Service{
		roots:    roots,
		logger:   logger,
		onChange: onChange,
		watcher:  fileWatcher,
	}, nil
}

func (s *Service) Start(ctx context.Context) error {
	defer s.watcher.Close()

	for _, root := range s.roots {
		if err := s.addRecursive(root); err != nil {
			return err
		}
	}
	s.logger.Info("markdown watcher started", "roots", strings.Join(s.roots, ","))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("markdown watcher stopped")
			return nil
		case event := <-s.watcher.Events:
			s.handleEvent(ctx, event)
		case err := <-s.watcher.Errors:
			if err != nil {
				s.logger.Error("file watcher error", "error", err)
			}
		}
	}
}

func (s *Service) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if err := s.watcher.Add(path); err != nil {
			return fmt.Errorf("watch path %s: %w", path, err)
		}
		return nil
	})
}

func (s *Service) handleEvent(ctx context.Context, event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if err := s.addRecursive(event.Name); err != nil {
				s.logger.Error("failed to add new directory to watcher", "path", event.Name, "error", err)
			}
			return
		}
	}
	if filepath.Ext(event.Name) != ".md" {
		return
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}
	s.logger.Info("markdown changed", "path", event.Name, "op", event.Op.String())
	s.onChange(ctx, event.Name)
}
