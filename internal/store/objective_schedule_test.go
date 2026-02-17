package store

import (
	"testing"
	"time"
)

func TestComputeScheduleNextRunWithCronExpr(t *testing.T) {
	from := time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)
	next, err := ComputeScheduleNextRun("0 * * * *", from)
	if err != nil {
		t.Fatalf("compute next run: %v", err)
	}
	expected := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected, next)
	}
}

func TestComputeScheduleNextRunRejectsInvalidCronExpr(t *testing.T) {
	_, err := ComputeScheduleNextRun("not-a-cron", time.Now().UTC())
	if err == nil {
		t.Fatal("expected invalid cron expression error")
	}
}
