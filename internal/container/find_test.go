package container

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindByLabels(t *testing.T) {
	tests := []struct {
		name           string
		folderSet      []string
		execOutput     string
		execErr        error
		wantContainers []Container
		wantErr        string
	}{
		{
			name:           "one container found (AC #1)",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\n",
			wantContainers: []Container{{ID: "abc123"}},
		},
		{
			name:           "no container found (AC #3)",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "",
			wantContainers: nil,
		},
		{
			name:           "multiple containers found",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\ndef456\n",
			wantContainers: []Container{{ID: "abc123"}, {ID: "def456"}},
		},
		{
			name:      "executor error propagated",
			folderSet: []string{"/home/user/project-a"},
			execErr:   errors.New("daemon not running"),
			wantErr:   "find containers",
		},
		{
			name:    "empty folder set",
			execErr: nil, // should not reach executor
			wantErr: "empty folder set",
		},
		{
			name:           "output with trailing whitespace",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\n\n",
			wantContainers: []Container{{ID: "abc123"}},
		},
		{
			name:           "output without trailing newline",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123",
			wantContainers: []Container{{ID: "abc123"}},
		},
		{
			name:           "malformed container ID skipped",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\nINVALID-ID\ndef456\n",
			wantContainers: []Container{{ID: "abc123"}, {ID: "def456"}},
		},
		{
			name:           "uppercase hex container ID rejected",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\nABC123\ndef456\n",
			wantContainers: []Container{{ID: "abc123"}, {ID: "def456"}},
		},
		{
			name:           "whitespace-only lines between IDs",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\n  \t\ndef456\n",
			wantContainers: []Container{{ID: "abc123"}, {ID: "def456"}},
		},
		{
			name:           "multi-folder set finds container",
			folderSet:      []string{"/home/user/A", "/home/user/B"},
			execOutput:     "abc123\n",
			wantContainers: []Container{{ID: "abc123"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.execOutput, err: tt.execErr}},
			}

			got, err := FindByLabels(t.Context(), exec, tt.folderSet)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("FindByLabels(ctx, exec, %v) = %v, want error containing %q",
						tt.folderSet, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("FindByLabels(ctx, exec, %v) error = %q, want error containing %q",
						tt.folderSet, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("FindByLabels(ctx, exec, %v) unexpected error: %v", tt.folderSet, err)
			}

			if diff := cmp.Diff(tt.wantContainers, got); diff != "" {
				t.Errorf("FindByLabels(ctx, exec, %v) mismatch (-want +got):\n%s",
					tt.folderSet, diff)
			}
		})
	}
}

func TestFindByLabels_CorrectArgs(t *testing.T) {
	exec := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}
	folderSet := []string{"/home/user/project-a"}

	_, err := FindByLabels(t.Context(), exec, folderSet)
	if err != nil {
		t.Fatalf("FindByLabels(ctx, exec, %v) unexpected error: %v", folderSet, err)
	}

	labels := NewLabels(folderSet)
	filterArgs := labels.FilterArgs()

	// Expect: ps --all <filter args> --format {{.ID}}
	want := append([]string{"ps", "--all"}, filterArgs...)
	want = append(want, "--format", "{{.ID}}")

	if len(exec.outputCalls) != 1 {
		t.Fatalf("FindByLabels made %d Output calls, want 1", len(exec.outputCalls))
	}
	if diff := cmp.Diff(want, exec.outputCalls[0]); diff != "" {
		t.Errorf("FindByLabels args mismatch (-want +got):\n%s", diff)
	}
}

func TestFindByAssistant(t *testing.T) {
	tests := []struct {
		name           string
		assistantName  string
		folderSet      []string
		execOutput     string
		execErr        error
		wantContainers []Container
		wantErr        string
	}{
		{
			name:           "one assistant container found",
			assistantName:  "claude",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\n",
			wantContainers: []Container{{ID: "abc123"}},
		},
		{
			name:           "no assistant container found",
			assistantName:  "claude",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "",
			wantContainers: nil,
		},
		{
			name:          "executor error propagated",
			assistantName: "claude",
			folderSet:     []string{"/home/user/project-a"},
			execErr:       errors.New("daemon not running"),
			wantErr:       "find assistant containers",
		},
		{
			name:          "empty folder set",
			assistantName: "claude",
			wantErr:       "empty folder set",
		},
		{
			name:      "empty assistant name",
			folderSet: []string{"/home/user/project-a"},
			wantErr:   "empty assistant name",
		},
		{
			name:           "multiple containers returned",
			assistantName:  "claude",
			folderSet:      []string{"/home/user/project-a"},
			execOutput:     "abc123\ndef456\n",
			wantContainers: []Container{{ID: "abc123"}, {ID: "def456"}},
		},
		{
			name:           "multi-folder set finds assistant container",
			assistantName:  "claude",
			folderSet:      []string{"/home/user/A", "/home/user/B"},
			execOutput:     "abc123\n",
			wantContainers: []Container{{ID: "abc123"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.execOutput, err: tt.execErr}},
			}

			got, err := FindByAssistant(t.Context(), exec, tt.assistantName, tt.folderSet)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("FindByAssistant(ctx, exec, %q, %v) = %v, want error containing %q",
						tt.assistantName, tt.folderSet, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("FindByAssistant(ctx, exec, %q, %v) error = %q, want error containing %q",
						tt.assistantName, tt.folderSet, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("FindByAssistant(ctx, exec, %q, %v) unexpected error: %v",
					tt.assistantName, tt.folderSet, err)
			}

			if diff := cmp.Diff(tt.wantContainers, got); diff != "" {
				t.Errorf("FindByAssistant(ctx, exec, %q, %v) mismatch (-want +got):\n%s",
					tt.assistantName, tt.folderSet, diff)
			}
		})
	}
}

func TestFindByAssistant_CorrectArgs(t *testing.T) {
	exec := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}

	_, err := FindByAssistant(t.Context(), exec, "claude", []string{"/home/user/project-a"})
	if err != nil {
		t.Fatalf("FindByAssistant() unexpected error: %v", err)
	}

	labels := NewAssistantLabels("claude", []string{"/home/user/project-a"})
	filterArgs := labels.FilterArgs()

	// Expect: ps --all <filter args> --format {{.ID}}
	want := append([]string{"ps", "--all"}, filterArgs...)
	want = append(want, "--format", "{{.ID}}")

	if len(exec.outputCalls) != 1 {
		t.Fatalf("FindByAssistant made %d Output calls, want 1", len(exec.outputCalls))
	}
	if diff := cmp.Diff(want, exec.outputCalls[0]); diff != "" {
		t.Errorf("FindByAssistant args mismatch (-want +got):\n%s", diff)
	}
}

func TestFindAllManaged(t *testing.T) {
	tests := []struct {
		name       string
		execOutput string
		execErr    error
		wantInfos  []ContainerInfo
		wantErr    string
	}{
		{
			name: "assistant and project-local containers",
			execOutput: "abc123\tUp 2 hours\t" +
				`{"devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project-a"}` + "\n" +
				"def456\tExited (0) 1 hour ago\t" +
				`{"devcontainer.local_folder":"/home/user/project-b"}` + "\n",
			wantInfos: []ContainerInfo{
				{ID: "abc123", Status: "Up 2 hours", Assistant: "claude", Workspace: "/home/user/project-a"},
				{ID: "def456", Status: "Exited (0) 1 hour ago", Assistant: "", Workspace: "/home/user/project-b"},
			},
		},
		{
			name:       "no containers",
			execOutput: "",
			wantInfos:  nil,
		},
		{
			name:    "executor error propagated",
			execErr: errors.New("daemon not running"),
			wantErr: "find managed containers",
		},
		{
			name: "single assistant container",
			execOutput: "abc123\tUp 5 minutes\t" +
				`{"devcontainer.assistant_name":"opencode","devcontainer.local_folder":"/home/user/repo"}` + "\n",
			wantInfos: []ContainerInfo{
				{ID: "abc123", Status: "Up 5 minutes", Assistant: "opencode", Workspace: "/home/user/repo"},
			},
		},
		{
			name: "malformed lines skipped",
			execOutput: "abc123\tUp 2 hours\t" +
				`{"devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project-a"}` + "\n" +
				"malformed-line\n" +
				"badjson\tUp\tnot-json\n" +
				"def456\tUp 1 hour\t" +
				`{"devcontainer.local_folder":"/home/user/project-b"}` + "\n",
			wantInfos: []ContainerInfo{
				{ID: "abc123", Status: "Up 2 hours", Assistant: "claude", Workspace: "/home/user/project-a"},
				{ID: "def456", Status: "Up 1 hour", Assistant: "", Workspace: "/home/user/project-b"},
			},
		},
		{
			name: "trailing whitespace handled",
			execOutput: "abc123\tUp 2 hours\t" +
				`{"devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project-a"}` + "\n\n",
			wantInfos: []ContainerInfo{
				{ID: "abc123", Status: "Up 2 hours", Assistant: "claude", Workspace: "/home/user/project-a"},
			},
		},
		{
			name: "extra labels ignored",
			execOutput: "abc123\tUp 2 hours\t" +
				`{"devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project-a","io.buildah.version":"1.33.7"}` + "\n",
			wantInfos: []ContainerInfo{
				{ID: "abc123", Status: "Up 2 hours", Assistant: "claude", Workspace: "/home/user/project-a"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.execOutput, err: tt.execErr}},
			}

			got, err := FindAllManaged(t.Context(), exec)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("FindAllManaged(ctx, exec) = %v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("FindAllManaged(ctx, exec) error = %q, want error containing %q",
						err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("FindAllManaged(ctx, exec) unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantInfos, got); diff != "" {
				t.Errorf("FindAllManaged(ctx, exec) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFindAllManaged_CorrectArgs(t *testing.T) {
	exec := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}

	_, err := FindAllManaged(t.Context(), exec)
	if err != nil {
		t.Fatalf("FindAllManaged() unexpected error: %v", err)
	}

	if len(exec.outputCalls) != 1 {
		t.Fatalf("FindAllManaged made %d Output calls, want 1", len(exec.outputCalls))
	}

	// Verify the args include the assistant-name filter and the portable
	// `{{json .Labels}}` format used by both Docker and Podman.
	args := exec.outputCalls[0]
	want := []string{
		"ps", "--all",
		"--filter", "label=devcontainer.assistant_name",
		"--format", `{{.ID}}	{{.Status}}	{{json .Labels}}`,
	}

	if diff := cmp.Diff(want, args); diff != "" {
		t.Errorf("FindAllManaged args mismatch (-want +got):\n%s", diff)
	}
}

func TestFindByLabels_DifferentFolderSets(t *testing.T) {
	// AC #2 and #5: Different folder sets produce different filter args.
	execA := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}
	execB := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}

	_, err := FindByLabels(t.Context(), execA, []string{"/home/user/project-a"})
	if err != nil {
		t.Fatalf("FindByLabels for project-a: %v", err)
	}

	_, err = FindByLabels(t.Context(), execB, []string{"/home/user/project-b"})
	if err != nil {
		t.Fatalf("FindByLabels for project-b: %v", err)
	}

	// The filter args must differ because the folder sets differ.
	if diff := cmp.Diff(execA.outputCalls[0], execB.outputCalls[0]); diff == "" {
		t.Error("FindByLabels produced identical args for different folder sets; want different filter args")
	}
}

func TestFindByLabels_SingleVsMultiFolderDifferentArgs(t *testing.T) {
	// AC #5: single-folder and multi-folder sets with same primary produce different args.
	execSingle := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}
	execMulti := &fakeMultiExecutor{
		outputResults: []outputResult{{output: ""}},
	}

	_, err := FindByLabels(t.Context(), execSingle, []string{"/home/user/project-a"})
	if err != nil {
		t.Fatalf("FindByLabels for single: %v", err)
	}

	_, err = FindByLabels(t.Context(), execMulti, []string{"/home/user/project-a", "/home/user/lib"})
	if err != nil {
		t.Fatalf("FindByLabels for multi: %v", err)
	}

	if diff := cmp.Diff(execSingle.outputCalls[0], execMulti.outputCalls[0]); diff == "" {
		t.Error("FindByLabels produced identical args for single-folder and multi-folder sets; want different filter args")
	}
}

func TestFindByAssistant_SamePrimaryDifferentFolderSetsCoexist(t *testing.T) {
	// REQ-MF-001 AC 10: `confine-ai claude . ../A` and `confine-ai claude .` (same primary,
	// different folder sets) produce different filter args, allowing two separate
	// containers to coexist.
	execSingle := &fakeMultiExecutor{
		outputResults: []outputResult{{output: "aabb0011\n"}},
	}
	execMulti := &fakeMultiExecutor{
		outputResults: []outputResult{{output: "ccdd0022\n"}},
	}

	primary := "/home/user/project"
	additional := "/home/user/A"

	singleContainers, err := FindByAssistant(t.Context(), execSingle, "claude", []string{primary})
	if err != nil {
		t.Fatalf("FindByAssistant(single folder) error: %v", err)
	}

	multiContainers, err := FindByAssistant(t.Context(), execMulti, "claude", []string{primary, additional})
	if err != nil {
		t.Fatalf("FindByAssistant(multi folder) error: %v", err)
	}

	// Both queries return results from their respective executors.
	if len(singleContainers) != 1 || singleContainers[0].ID != "aabb0011" {
		t.Errorf("FindByAssistant(single) = %v, want [{aabb0011}]", singleContainers)
	}
	if len(multiContainers) != 1 || multiContainers[0].ID != "ccdd0022" {
		t.Errorf("FindByAssistant(multi) = %v, want [{ccdd0022}]", multiContainers)
	}

	// The filter args must differ because the folder sets differ.
	// This proves the runtime would query for different containers.
	if diff := cmp.Diff(execSingle.outputCalls[0], execMulti.outputCalls[0]); diff == "" {
		t.Error("FindByAssistant produced identical args for single-folder and multi-folder sets with same primary; want different filter args to allow coexistence")
	}
}

func TestFindByAssistant_ArgumentOrderIndependence(t *testing.T) {
	// REQ-MF-001 AC 11: `confine-ai claude . ../A` and `confine-ai claude ../A .`
	// (same folders, different argument order) produce the same filter args,
	// finding the same container.
	execAB := &fakeMultiExecutor{
		outputResults: []outputResult{{output: "aabb0011\n"}},
	}
	execBA := &fakeMultiExecutor{
		outputResults: []outputResult{{output: "aabb0011\n"}},
	}

	folderA := "/home/user/project"
	folderB := "/home/user/A"

	containersAB, err := FindByAssistant(t.Context(), execAB, "claude", []string{folderA, folderB})
	if err != nil {
		t.Fatalf("FindByAssistant([A,B]) error: %v", err)
	}

	containersBA, err := FindByAssistant(t.Context(), execBA, "claude", []string{folderB, folderA})
	if err != nil {
		t.Fatalf("FindByAssistant([B,A]) error: %v", err)
	}

	// Both should find the same container.
	if len(containersAB) != 1 || len(containersBA) != 1 {
		t.Fatalf("FindByAssistant returned %d and %d containers, want 1 each",
			len(containersAB), len(containersBA))
	}

	// The filter args must be identical because the folder sets are the same
	// (just reordered). This proves argument-order independence at the query level.
	if diff := cmp.Diff(execAB.outputCalls[0], execBA.outputCalls[0]); diff != "" {
		t.Errorf("FindByAssistant produced different args for same folders in different order; want identical (argument-order independence):\n%s", diff)
	}
}
