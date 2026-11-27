package brain

import (
	"strings"
	"testing"
	"time"
)

func TestJobClockReportsElapsedTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	c := newJobClock(start)
	if line := c.observe(start.Add(12 * time.Second)); line != "[12s since the job started, 12s since your previous action.]" {
		t.Fatalf("first: %q", line)
	}
	if line := c.observe(start.Add(70 * time.Second)); line != "[1m 10s since the job started, 58s since your previous action.]" {
		t.Fatalf("second: %q", line)
	}
}

func TestRulesExplainTheTimingAndTheStuckFallback(t *testing.T) {
	for _, want := range []string{"since the job started", "title bar", "list_running_applications"} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules don't mention %q", want)
		}
	}
}
