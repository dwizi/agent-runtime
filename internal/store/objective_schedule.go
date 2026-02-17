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

const objectiveDefaultTimezone = "UTC"

func normalizeCronExpr(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

// ComputeScheduleNextRun resolves the next run timestamp for schedule objectives.
func ComputeScheduleNextRun(cronExpr string, from time.Time) (time.Time, error) {
	return ComputeScheduleNextRunForTimezone(cronExpr, objectiveDefaultTimezone, from)
}

// ComputeScheduleNextRunForTimezone resolves the next run timestamp in the provided IANA timezone.
func ComputeScheduleNextRunForTimezone(cronExpr, timezone string, from time.Time) (time.Time, error) {
	cronExpr = normalizeCronExpr(cronExpr)
	base := from
	if base.IsZero() {
		base = time.Now().UTC()
	}

	if cronExpr == "" {
		return time.Time{}, nil
	}
	normalizedTimezone, err := normalizeObjectiveTimezone(timezone)
	if err != nil {
		return time.Time{}, err
	}
	location, err := time.LoadLocation(normalizedTimezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone: %w", err)
	}
	spec, err := objectiveCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron expression: %w", err)
	}
	return spec.Next(base.In(location)).UTC(), nil
}

func normalizeObjectiveTimezone(raw string) (string, error) {
	timezone := strings.TrimSpace(raw)
	if timezone == "" {
		timezone = objectiveDefaultTimezone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", fmt.Errorf("invalid timezone: %w", err)
	}
	return timezone, nil
}
