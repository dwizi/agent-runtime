package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var objectiveCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

func normalizeCronExpr(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

// ComputeScheduleNextRun resolves the next run timestamp for schedule objectives.
func ComputeScheduleNextRun(cronExpr string, from time.Time) (time.Time, error) {
	cronExpr = normalizeCronExpr(cronExpr)
	base := from
	if base.IsZero() {
		base = time.Now()
	}

	if cronExpr == "" {
		return time.Time{}, nil
	}
	spec, err := objectiveCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron expression: %w", err)
	}
	return spec.Next(base), nil
}
