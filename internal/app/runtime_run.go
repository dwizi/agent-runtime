package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dwizi/agent-runtime/internal/heartbeat"
)

func (r *Runtime) Run(ctx context.Context) error {
	r.logger.Info("agent-runtime runtime starting", "addr", r.cfg.HTTPAddr, "workspace_root", r.cfg.WorkspaceRoot)
	if r.heartbeat != nil {
		r.heartbeat.Beat("runtime", "runtime loop started")
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if r.heartbeat != nil {
			r.heartbeat.Starting("orchestrator", "workers starting")
		}
		return runMonitored(groupCtx, r.heartbeat, "orchestrator", 20*time.Second, func(runCtx context.Context) error {
			return r.engine.Start(runCtx)
		})
	})
	recoveryStaleAfter := time.Duration(r.cfg.TaskRecoveryRunningStaleSec) * time.Second
	if err := recoverPendingTasks(groupCtx, r.store, r.engine, recoveryStaleAfter, r.logger.With("component", "task-recovery")); err != nil {
		r.logger.Error("startup task recovery failed", "error", err)
	}
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "task-recovery", 20*time.Second, func(runCtx context.Context) error {
			return runStaleTaskRecoveryLoop(runCtx, r.store, r.engine, recoveryStaleAfter, r.logger.With("component", "task-recovery-loop"))
		})
	})
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "watcher", 0, func(runCtx context.Context) error {
			return r.watcher.Start(runCtx)
		})
	})
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "scheduler", 0, func(runCtx context.Context) error {
			return r.scheduler.Start(runCtx)
		})
	})
	for _, conn := range r.connectors {
		connector := conn
		group.Go(func() error {
			componentName := "connector:" + strings.ToLower(strings.TrimSpace(connector.Name()))
			return runMonitored(groupCtx, r.heartbeat, componentName, 0, func(runCtx context.Context) error {
				return connector.Start(runCtx)
			})
		})
	}
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "api", 20*time.Second, func(runCtx context.Context) error {
			err := r.httpServer.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	})
	if r.heartbeatMonitor != nil {
		group.Go(func() error {
			return r.heartbeatMonitor.Start(groupCtx)
		})
	}
	group.Go(func() error {
		<-groupCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return r.httpServer.Shutdown(shutdownCtx)
	})

	return group.Wait()
}

func (r *Runtime) Close() error {
	if r.qmd != nil {
		r.qmd.Close()
	}
	if r.store == nil {
		return nil
	}
	return r.store.Close()
}

func runMonitored(
	ctx context.Context,
	reporter heartbeat.Reporter,
	component string,
	beatInterval time.Duration,
	run func(context.Context) error,
) error {
	if run == nil {
		return nil
	}
	if reporter != nil {
		reporter.Starting(component, "starting")
		reporter.Beat(component, "running")
	}

	var stopHeartbeat func()
	if reporter != nil && beatInterval > 0 {
		heartbeatCtx, cancel := context.WithCancel(ctx)
		stopHeartbeat = cancel
		go func() {
			ticker := time.NewTicker(beatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatCtx.Done():
					return
				case <-ticker.C:
					reporter.Beat(component, "running")
				}
			}
		}()
	}

	err := run(ctx)
	if stopHeartbeat != nil {
		stopHeartbeat()
	}
	if reporter == nil {
		return err
	}
	if err != nil && ctx.Err() == nil {
		reporter.Degrade(component, "component failed", err)
		return err
	}
	reporter.Stopped(component, "stopped")
	return err
}
