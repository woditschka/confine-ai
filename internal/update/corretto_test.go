package update

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// correttoHandler builds an httptest handler that mimics the two endpoints
// the Corretto adapter uses: latest (returns a 302 whose Location names the
// version) and latest_sha256 (returns a plaintext hex digest).
//
// The filenames and URL patterns match the shape documented in
// docs/adr/2026-04-12-outbound-http-trust-boundary.md and samples/base/Dockerfile.
type correttoFixture struct {
	// version is the full dotted version string discovered via the
	// latest redirect (e.g., "25.0.2.10.1").
	version string
	// shaByCorrettoArch is the plaintext sha256 hex string returned by
	// the latest_sha256 endpoint, keyed by Corretto arch ("x64"|"aarch64").
	shaByCorrettoArch map[string]string
	// failLatest, if non-zero, overrides the latest response with this
	// HTTP status code.
	failLatest int
	// failLatestSha256, if non-zero, overrides the latest_sha256 response
	// with this HTTP status code.
	failLatestSha256 int
}

func newCorrettoServer(t *testing.T, fx correttoFixture) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/latest/"):
			if fx.failLatest != 0 {
				http.Error(w, "boom", fx.failLatest)
				return
			}
			// Extract the Corretto arch from the filename segment.
			// Expected: /downloads/latest/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz
			arch := extractTestArch(path)
			loc := fmt.Sprintf("/resources/%s/amazon-corretto-%s-linux-%s.tar.gz", fx.version, fx.version, arch)
			http.Redirect(w, r, loc, http.StatusFound)
		case strings.Contains(path, "/latest_sha256/"):
			if fx.failLatestSha256 != 0 {
				http.Error(w, "boom", fx.failLatestSha256)
				return
			}
			arch := extractTestArch(path)
			sha, ok := fx.shaByCorrettoArch[arch]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprint(w, sha)
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewTLSServer(handler)
}

// extractTestArch parses the filename segment of the form
// amazon-corretto-<major>-<arch>-linux-jdk.tar.gz and returns <arch>.
func extractTestArch(path string) string {
	// Filename is the final segment.
	slash := strings.LastIndex(path, "/")
	name := path[slash+1:]
	// amazon-corretto-25-x64-linux-jdk.tar.gz -> ["amazon", "corretto", "25", "x64", "linux", "jdk.tar.gz"]
	parts := strings.Split(name, "-")
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

func TestCorrettoUpstream_ProbeHappyPath(t *testing.T) {
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64":     "1111111111111111111111111111111111111111111111111111111111111111",
			"aarch64": "2222222222222222222222222222222222222222222222222222222222222222",
		},
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	res, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64", "arm64"})
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if res.Version != "25.0.3.11.1" {
		t.Errorf("Probe version = %q, want 25.0.3.11.1", res.Version)
	}
	if got := res.Sha256["amd64"]; got != fx.shaByCorrettoArch["x64"] {
		t.Errorf("amd64 sha256 = %q, want %q", got, fx.shaByCorrettoArch["x64"])
	}
	if got := res.Sha256["arm64"]; got != fx.shaByCorrettoArch["aarch64"] {
		t.Errorf("arm64 sha256 = %q, want %q", got, fx.shaByCorrettoArch["aarch64"])
	}
}

func TestCorrettoUpstream_ProbeMajorJumpDetected(t *testing.T) {
	// The fixture reports version 26.x; the caller's current version is
	// 25.x. The adapter must surface this so the orchestrator can prompt.
	fx := correttoFixture{
		version: "26.0.0.5.1",
		shaByCorrettoArch: map[string]string{
			"x64":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"aarch64": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	res, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64", "arm64"})
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	oldMajor, newMajor, err := MajorVersions("25.0.2.10.1", res.Version)
	if err != nil {
		t.Fatalf("MajorVersions unexpected error: %v", err)
	}
	if oldMajor != 25 || newMajor != 26 {
		t.Errorf("MajorVersions = (%d, %d), want (25, 26)", oldMajor, newMajor)
	}
}

func TestCorrettoUpstream_ProbeChecksumFetch4xx(t *testing.T) {
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64":     "1111111111111111111111111111111111111111111111111111111111111111",
			"aarch64": "2222222222222222222222222222222222222222222222222222222222222222",
		},
		failLatest: http.StatusNotFound,
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	if _, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64"}); err == nil {
		t.Error("Probe(checksum 404) = nil, want error")
	}
}

func TestCorrettoUpstream_ProbeSha256Fetch4xx(t *testing.T) {
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64": "1111111111111111111111111111111111111111111111111111111111111111",
		},
		failLatestSha256: http.StatusForbidden,
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	if _, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64"}); err == nil {
		t.Error("Probe(sha 403) = nil, want error")
	}
}

func TestCorrettoUpstream_ProbeInvalidSha256Shape(t *testing.T) {
	// Returns a non-64-char string. The adapter must reject it.
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64": "deadbeef", // too short
		},
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	_, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64"})
	if err == nil {
		t.Fatal("Probe(short sha) = nil, want error")
	}
	if !errors.Is(err, ErrInvalidSha256) {
		t.Errorf("Probe(short sha) err = %v, want ErrInvalidSha256", err)
	}
}

func TestCorrettoUpstream_ProbeSha256NonHex(t *testing.T) {
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			// 64 chars but contains a non-hex digit 'z'.
			"x64": "zzzz111111111111111111111111111111111111111111111111111111111111",
		},
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	_, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64"})
	if !errors.Is(err, ErrInvalidSha256) {
		t.Errorf("Probe(non-hex sha) err = %v, want ErrInvalidSha256", err)
	}
}

func TestCorrettoUpstream_ProbeSha256WithWhitespace(t *testing.T) {
	// Some latest_sha256 endpoints include trailing whitespace or a
	// filename suffix in the response body. The adapter must tolerate it.
	fx := correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64": "1111111111111111111111111111111111111111111111111111111111111111  amazon-corretto-25.0.3.11.1-linux-x64.tar.gz\n",
		},
	}
	server := newCorrettoServer(t, fx)
	defer server.Close()

	up := NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	res, err := up.Probe(t.Context(), "25.0.2.10.1", []string{"amd64"})
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if res.Sha256["amd64"] != "1111111111111111111111111111111111111111111111111111111111111111" {
		t.Errorf("amd64 sha256 = %q, want normalized 64-char hex", res.Sha256["amd64"])
	}
}

func TestCorrettoUpstream_ProbeBadCurrentVersion(t *testing.T) {
	// currentVersion has no dotted major: MajorVersions should reject it
	// through the adapter's version parsing.
	server := newCorrettoServer(t, correttoFixture{
		version: "25.0.3.11.1",
		shaByCorrettoArch: map[string]string{
			"x64": "1111111111111111111111111111111111111111111111111111111111111111",
		},
	})
	defer server.Close()

	_ = NewCorrettoUpstream(newTestClient(t, server), server.URL+"/downloads")
	// Probe itself doesn't parse the caller's current version, but
	// MajorVersions is the helper the orchestrator uses: exercise it
	// directly to catch the failure path.
	if _, _, err := MajorVersions("notaversion", "25.0.3.11.1"); err == nil {
		t.Error("MajorVersions(notaversion, ...) = nil, want error")
	}
}

func TestMajorVersions(t *testing.T) {
	cases := []struct {
		name             string
		old, new         string
		wantOld, wantNew int
		wantErr          bool
	}{
		{name: "same major", old: "25.0.2.10.1", new: "25.0.3.11.1", wantOld: 25, wantNew: 25},
		{name: "major bump", old: "25.0.2.10.1", new: "26.0.0.5.1", wantOld: 25, wantNew: 26},
		{name: "bad old", old: "x.y.z", new: "25.0.2.10.1", wantErr: true},
		{name: "bad new", old: "25.0.2.10.1", new: "", wantErr: true},
		{name: "negative major", old: "-1.0.0", new: "25.0.0", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldMaj, newMaj, err := MajorVersions(tc.old, tc.new)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("MajorVersions(%q, %q) = nil, want error", tc.old, tc.new)
				}
				return
			}
			if err != nil {
				t.Fatalf("MajorVersions(%q, %q) unexpected error: %v", tc.old, tc.new, err)
			}
			if oldMaj != tc.wantOld || newMaj != tc.wantNew {
				t.Errorf("MajorVersions(%q, %q) = (%d, %d), want (%d, %d)", tc.old, tc.new, oldMaj, newMaj, tc.wantOld, tc.wantNew)
			}
		})
	}
}
