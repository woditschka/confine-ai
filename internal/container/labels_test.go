package container

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// wantFolderSetID computes the expected folderSetID for sorted paths using the
// length-prefix + null-byte encoding. Test helper only.
func wantFolderSetID(sortedPaths ...string) string {
	var b strings.Builder
	for _, p := range sortedPaths {
		fmt.Fprintf(&b, "%d:%s\x00", len(p), p)
	}
	hash := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(hash[:])
}

// hexSHA256 computes the lowercase hex SHA-256 of s. Test helper only.
func hexSHA256(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

func TestNewLabels(t *testing.T) {
	tests := []struct {
		name            string
		folderSet       []string
		wantLocalFolder string
		wantMetadataID  string
	}{
		{
			name:            "single folder",
			folderSet:       []string{"/home/user/project-a"},
			wantLocalFolder: "/home/user/project-a",
			wantMetadataID:  wantFolderSetID("/home/user/project-a"),
		},
		{
			name:            "root path",
			folderSet:       []string{"/"},
			wantLocalFolder: "/",
			wantMetadataID:  wantFolderSetID("/"),
		},
		{
			name:            "path with spaces",
			folderSet:       []string{"/home/user/my project"},
			wantLocalFolder: "/home/user/my project",
			wantMetadataID:  wantFolderSetID("/home/user/my project"),
		},
		{
			name:            "path with control characters sanitized in local_folder",
			folderSet:       []string{"/home/user/pro\x01ject"},
			wantLocalFolder: "/home/user/pro\ufffdject",
			wantMetadataID:  wantFolderSetID("/home/user/pro\x01ject"),
		},
		{
			name:            "two folders sorted and joined",
			folderSet:       []string{"/home/user/B", "/home/user/A"},
			wantLocalFolder: "/home/user/A\n/home/user/B",
			wantMetadataID:  wantFolderSetID("/home/user/A", "/home/user/B"),
		},
		{
			name:            "argument-order independence (AC 4)",
			folderSet:       []string{"/home/user/A", "/home/user/B"},
			wantLocalFolder: "/home/user/A\n/home/user/B",
			wantMetadataID:  wantFolderSetID("/home/user/A", "/home/user/B"),
		},
		{
			name:            "three folders sorted",
			folderSet:       []string{"/z", "/a", "/m"},
			wantLocalFolder: "/a\n/m\n/z",
			wantMetadataID:  wantFolderSetID("/a", "/m", "/z"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := NewLabels(tt.folderSet)

			got := labels.Values()
			if got[labelLocalFolder] != tt.wantLocalFolder {
				t.Errorf("NewLabels(%v).Values()[%q] = %q, want %q",
					tt.folderSet, labelLocalFolder, got[labelLocalFolder], tt.wantLocalFolder)
			}
			if got[labelMetadataID] != tt.wantMetadataID {
				t.Errorf("NewLabels(%v).Values()[%q] = %q, want %q",
					tt.folderSet, labelMetadataID, got[labelMetadataID], tt.wantMetadataID)
			}
		})
	}
}

func TestNewLabels_ArgumentOrderIndependence(t *testing.T) {
	// REQ-CO-001 AC 4: same folders in different order produce same identity.
	labelsAB := NewLabels([]string{"/home/user/A", "/home/user/B"})
	labelsBA := NewLabels([]string{"/home/user/B", "/home/user/A"})

	if labelsAB.Values()[labelMetadataID] != labelsBA.Values()[labelMetadataID] {
		t.Errorf("NewLabels([A,B]).metadata_id = %q, NewLabels([B,A]).metadata_id = %q; want equal",
			labelsAB.Values()[labelMetadataID], labelsBA.Values()[labelMetadataID])
	}
}

func TestNewLabels_DifferentFolderSetsDifferentIDs(t *testing.T) {
	// REQ-CO-001 AC 5: single-folder and multi-folder sets with same primary produce different IDs.
	labelsSingle := NewLabels([]string{"/home/user/project-a"})
	labelsMulti := NewLabels([]string{"/home/user/project-a", "/home/user/lib"})

	if labelsSingle.Values()[labelMetadataID] == labelsMulti.Values()[labelMetadataID] {
		t.Error("NewLabels([project-a]) and NewLabels([project-a, lib]) produced same metadata_id; want different")
	}
}

func TestNewLabels_SHA256Consistency(t *testing.T) {
	// Verify the SHA-256 computation matches the folderSetID algorithm:
	// length-prefix + null-byte encoding.
	folders := []string{"/home/user/project-a"}
	labels := NewLabels(folders)

	want := wantFolderSetID("/home/user/project-a")

	got := labels.Values()[labelMetadataID]
	if got != want {
		t.Errorf("NewLabels(%v) metadata_id = %q, want %q (SHA-256 mismatch)", folders, got, want)
	}
}

func TestNewLabels_NewlineInPathNoCollision(t *testing.T) {
	// Security: a path containing a newline must not collide with two separate paths.
	singleWithNewline := NewLabels([]string{"/a\n/b"})
	twoSeparate := NewLabels([]string{"/a", "/b"})

	if singleWithNewline.Values()[labelMetadataID] == twoSeparate.Values()[labelMetadataID] {
		t.Error("path with embedded newline collides with two separate paths; length-prefix encoding should prevent this")
	}
}

func TestLabels_ForArgs(t *testing.T) {
	labels := NewLabels([]string{"/home/user/project-a"})
	got := labels.ForArgs()

	want := []string{
		"--label", "devcontainer.local_folder=/home/user/project-a",
		"--label", "devcontainer.metadata_id=" + wantFolderSetID("/home/user/project-a"),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Labels.ForArgs() mismatch (-want +got):\n%s", diff)
	}
}

func TestLabels_ForArgs_MultiFolders(t *testing.T) {
	labels := NewLabels([]string{"/home/user/B", "/home/user/A"})
	got := labels.ForArgs()

	want := []string{
		"--label", "devcontainer.local_folder=/home/user/A\n/home/user/B",
		"--label", "devcontainer.metadata_id=" + wantFolderSetID("/home/user/A", "/home/user/B"),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Labels.ForArgs() mismatch (-want +got):\n%s", diff)
	}
}

func TestLabels_FilterArgs(t *testing.T) {
	labels := NewLabels([]string{"/home/user/project-a"})
	got := labels.FilterArgs()

	want := []string{
		"--filter", "label=devcontainer.metadata_id=" + wantFolderSetID("/home/user/project-a"),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Labels.FilterArgs() mismatch (-want +got):\n%s", diff)
	}
}

func TestNewAssistantLabels(t *testing.T) {
	labels := NewAssistantLabels("claude", []string{"/home/user/project-a"})
	got := labels.Values()

	if len(got) != 3 {
		t.Fatalf("NewAssistantLabels() Values() has %d entries, want 3", len(got))
	}

	wantLocalFolder := "/home/user/project-a"
	if got[labelLocalFolder] != wantLocalFolder {
		t.Errorf("NewAssistantLabels().Values()[%q] = %q, want %q",
			labelLocalFolder, got[labelLocalFolder], wantLocalFolder)
	}

	wantMetadataID := wantFolderSetID("/home/user/project-a")
	if got[labelMetadataID] != wantMetadataID {
		t.Errorf("NewAssistantLabels().Values()[%q] = %q, want %q",
			labelMetadataID, got[labelMetadataID], wantMetadataID)
	}

	wantAssistantName := "claude"
	if got[labelAssistantName] != wantAssistantName {
		t.Errorf("NewAssistantLabels().Values()[%q] = %q, want %q",
			labelAssistantName, got[labelAssistantName], wantAssistantName)
	}
}

func TestNewAssistantLabels_MultiFolders(t *testing.T) {
	labels := NewAssistantLabels("claude", []string{"/home/user/B", "/home/user/A"})
	got := labels.Values()

	wantLocalFolder := "/home/user/A\n/home/user/B"
	if got[labelLocalFolder] != wantLocalFolder {
		t.Errorf("NewAssistantLabels().Values()[%q] = %q, want %q",
			labelLocalFolder, got[labelLocalFolder], wantLocalFolder)
	}

	wantMetadataID := wantFolderSetID("/home/user/A", "/home/user/B")
	if got[labelMetadataID] != wantMetadataID {
		t.Errorf("NewAssistantLabels().Values()[%q] = %q, want %q",
			labelMetadataID, got[labelMetadataID], wantMetadataID)
	}
}

func TestAssistantLabelsForArgs(t *testing.T) {
	labels := NewAssistantLabels("claude", []string{"/home/user/project-a"})
	got := labels.ForArgs()

	want := []string{
		"--label", "devcontainer.local_folder=/home/user/project-a",
		"--label", "devcontainer.metadata_id=" + wantFolderSetID("/home/user/project-a"),
		"--label", "devcontainer.assistant_name=claude",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("AssistantLabels.ForArgs() mismatch (-want +got):\n%s", diff)
	}
}

func TestAssistantLabelsFilterArgs(t *testing.T) {
	labels := NewAssistantLabels("claude", []string{"/home/user/project-a"})
	got := labels.FilterArgs()

	want := []string{
		"--filter", "label=devcontainer.metadata_id=" + wantFolderSetID("/home/user/project-a"),
		"--filter", "label=devcontainer.assistant_name=claude",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("AssistantLabels.FilterArgs() mismatch (-want +got):\n%s", diff)
	}
}

func TestFolderSetID_DiffersFromOldScheme(t *testing.T) {
	// Verify that the new encoding differs from the old workspaceID scheme
	// (plain SHA-256 of the path string).
	singleFolder := folderSetID([]string{"/home/user/project"})
	oldStyleHash := hexSHA256("/home/user/project")

	if singleFolder == oldStyleHash {
		t.Error("folderSetID matches old workspaceID hash; want different (encoding change)")
	}
}

func TestFolderSetID_SameInputSameOutput(t *testing.T) {
	// folderSetID expects pre-sorted input. Verify determinism.
	id1 := folderSetID([]string{"/a", "/b", "/c"})
	id2 := folderSetID([]string{"/a", "/b", "/c"})

	if id1 != id2 {
		t.Errorf("folderSetID is not deterministic: %q != %q", id1, id2)
	}
}

func TestNewLabels_OrderIndependenceViaNewLabels(t *testing.T) {
	// NewLabels sorts internally, so different orderings produce the same ID.
	l1 := NewLabels([]string{"/c", "/a", "/b"})
	l2 := NewLabels([]string{"/a", "/b", "/c"})
	l3 := NewLabels([]string{"/b", "/c", "/a"})

	id1 := l1.Values()[labelMetadataID]
	id2 := l2.Values()[labelMetadataID]
	id3 := l3.Values()[labelMetadataID]

	if id1 != id2 || id2 != id3 {
		t.Errorf("NewLabels is not order-independent: %q, %q, %q", id1, id2, id3)
	}
}
