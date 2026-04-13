package update

import (
	"strings"
	"testing"
)

func TestAggregate_ExitCodeMaxSeverity(t *testing.T) {
	cases := []struct {
		name     string
		results  []TargetResult
		wantExit int
	}{
		{
			name:     "empty",
			results:  nil,
			wantExit: 0,
		},
		{
			name: "single success",
			results: []TargetResult{
				{Target: "base", Action: ActionUpdated, ExitCode: 0},
			},
			wantExit: 0,
		},
		{
			name: "single unchanged is success",
			results: []TargetResult{
				{Target: "base", Action: ActionUnchanged, ExitCode: 0},
			},
			wantExit: 0,
		},
		{
			name: "single skipped is success",
			results: []TargetResult{
				{Target: "base", Action: ActionSkipped, ExitCode: 0},
			},
			wantExit: 0,
		},
		{
			name: "single failed generic",
			results: []TargetResult{
				{Target: "base", Action: ActionFailed, ExitCode: 1},
			},
			wantExit: 1,
		},
		{
			name: "probe failure exit 2",
			results: []TargetResult{
				{Target: "base", Action: ActionFailed, ExitCode: 2},
			},
			wantExit: 2,
		},
		{
			name: "sha failure exit 3",
			results: []TargetResult{
				{Target: "base", Action: ActionFailed, ExitCode: 3},
			},
			wantExit: 3,
		},
		{
			name: "abort exit 4",
			results: []TargetResult{
				{Target: "base", Action: ActionFailed, ExitCode: 4},
			},
			wantExit: 4,
		},
		{
			name: "mixed 1 and 3 picks 3",
			results: []TargetResult{
				{Target: "base", Action: ActionFailed, ExitCode: 1},
				{Target: "claude", Action: ActionFailed, ExitCode: 3},
			},
			wantExit: 3,
		},
		{
			name: "success plus probe failure picks 2",
			results: []TargetResult{
				{Target: "base", Action: ActionUpdated, ExitCode: 0},
				{Target: "claude", Action: ActionFailed, ExitCode: 2},
			},
			wantExit: 2,
		},
		{
			name: "4 beats 3 beats 2 beats 1",
			results: []TargetResult{
				{Target: "a", Action: ActionFailed, ExitCode: 1},
				{Target: "b", Action: ActionFailed, ExitCode: 2},
				{Target: "c", Action: ActionFailed, ExitCode: 3},
				{Target: "d", Action: ActionFailed, ExitCode: 4},
			},
			wantExit: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Aggregate(tc.results)
			if got != tc.wantExit {
				t.Errorf("Aggregate(%v) exit = %d, want %d", tc.results, got, tc.wantExit)
			}
		})
	}
}

func TestAggregate_SummaryText(t *testing.T) {
	results := []TargetResult{
		{
			Target: "base",
			Action: ActionUpdated,
			GroupDeltas: []GroupDelta{
				{Tool: "go", OldVersion: "1.26.0", NewVersion: "1.27.1"},
				{Tool: "java", Distribution: "corretto", OldVersion: "25.0.2.10.1", NewVersion: "25.0.3.11.1"},
			},
			ExitCode: 0,
		},
		{
			Target:   "claude",
			Action:   ActionUnchanged,
			ExitCode: 0,
		},
	}
	_, summary := Aggregate(results)
	if !strings.Contains(summary, "base") {
		t.Errorf("summary missing base: %q", summary)
	}
	if !strings.Contains(summary, "updated") {
		t.Errorf("summary missing updated action: %q", summary)
	}
	if !strings.Contains(summary, "1.26.0") || !strings.Contains(summary, "1.27.1") {
		t.Errorf("summary missing go version transition: %q", summary)
	}
	if !strings.Contains(summary, "claude") {
		t.Errorf("summary missing claude: %q", summary)
	}
	if !strings.Contains(summary, "unchanged") {
		t.Errorf("summary missing unchanged action: %q", summary)
	}
}

func TestAggregate_SummaryFailureIncludesError(t *testing.T) {
	results := []TargetResult{
		{
			Target:   "base",
			Action:   ActionFailed,
			ExitCode: 3,
			Error:    "corretto sha256 fetch: 404",
		},
	}
	_, summary := Aggregate(results)
	if !strings.Contains(summary, "failed") {
		t.Errorf("summary missing failed action: %q", summary)
	}
	if !strings.Contains(summary, "corretto sha256 fetch") {
		t.Errorf("summary missing error detail: %q", summary)
	}
}

func TestAggregate_WouldUpdateActionReported(t *testing.T) {
	results := []TargetResult{
		{
			Target: "base",
			Action: ActionWouldUpdate,
			GroupDeltas: []GroupDelta{
				{Tool: "go", OldVersion: "1.26.0", NewVersion: "1.27.1"},
			},
			ExitCode: 0,
		},
	}
	exit, summary := Aggregate(results)
	if exit != 0 {
		t.Errorf("Aggregate exit = %d, want 0", exit)
	}
	if !strings.Contains(summary, "would update") {
		t.Errorf("summary missing would update: %q", summary)
	}
}
