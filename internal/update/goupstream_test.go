package update

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// goFixtureHappy is a trimmed but representative slice of the JSON that
// go.dev/dl/?mode=json returns. The fixture is deliberately minimal so the
// adapter's tolerance to unknown fields is exercised (kind=source entries,
// "size" fields, etc. are absent).
const goFixtureHappy = `[
  {
    "version": "go1.27.1",
    "stable": true,
    "files": [
      {"filename": "go1.27.1.linux-amd64.tar.gz", "os": "linux", "arch": "amd64", "kind": "archive", "sha256": "aaaa0001aaaa0002aaaa0003aaaa0004aaaa0005aaaa0006aaaa0007aaaa0008"},
      {"filename": "go1.27.1.linux-arm64.tar.gz", "os": "linux", "arch": "arm64", "kind": "archive", "sha256": "bbbb0001bbbb0002bbbb0003bbbb0004bbbb0005bbbb0006bbbb0007bbbb0008"},
      {"filename": "go1.27.1.darwin-amd64.tar.gz", "os": "darwin", "arch": "amd64", "kind": "archive", "sha256": "cccc0001cccc0002cccc0003cccc0004cccc0005cccc0006cccc0007cccc0008"}
    ]
  },
  {
    "version": "go1.27rc1",
    "stable": false,
    "files": []
  }
]`

func TestGoUpstream_ProbeHappyPath(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "mode=json") {
			t.Errorf("request query = %q, want mode=json", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(goFixtureHappy))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	up := NewGoUpstream(c, server.URL+"/dl/")
	res, err := up.Probe(t.Context(), []string{"amd64", "arm64"})
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if res.Version != "1.27.1" {
		t.Errorf("Probe version = %q, want 1.27.1", res.Version)
	}
	if got := res.Sha256["amd64"]; got != "aaaa0001aaaa0002aaaa0003aaaa0004aaaa0005aaaa0006aaaa0007aaaa0008" {
		t.Errorf("amd64 sha256 = %q, want aaaa...0008", got)
	}
	if got := res.Sha256["arm64"]; got != "bbbb0001bbbb0002bbbb0003bbbb0004bbbb0005bbbb0006bbbb0007bbbb0008" {
		t.Errorf("arm64 sha256 = %q, want bbbb...0008", got)
	}
}

func TestGoUpstream_ProbeSkipsUnstable(t *testing.T) {
	// The first entry is unstable; only the second is stable. The adapter
	// must skip to the first stable entry rather than returning the top of
	// the list.
	body := `[
  {"version": "go1.28beta1", "stable": false, "files": []},
  {"version": "go1.27.1", "stable": true, "files": [
     {"filename": "go1.27.1.linux-amd64.tar.gz", "os": "linux", "arch": "amd64", "kind": "archive", "sha256": "1111111111111111111111111111111111111111111111111111111111111111"},
     {"filename": "go1.27.1.linux-arm64.tar.gz", "os": "linux", "arch": "arm64", "kind": "archive", "sha256": "2222222222222222222222222222222222222222222222222222222222222222"}
  ]}
]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	res, err := up.Probe(t.Context(), []string{"amd64", "arm64"})
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if res.Version != "1.27.1" {
		t.Errorf("Probe version = %q, want 1.27.1", res.Version)
	}
}

func TestGoUpstream_ProbeServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	_, err := up.Probe(t.Context(), []string{"amd64"})
	if err == nil {
		t.Fatal("Probe(500) = nil, want error")
	}
}

func TestGoUpstream_ProbeMalformedJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	_, err := up.Probe(t.Context(), []string{"amd64"})
	if err == nil {
		t.Fatal("Probe(malformed) = nil, want error")
	}
}

func TestGoUpstream_ProbeMissingArch(t *testing.T) {
	// Stable release exists but has no arm64 archive entry. The adapter
	// must return an error naming the missing arch.
	body := `[
  {"version": "go1.27.1", "stable": true, "files": [
     {"filename": "go1.27.1.linux-amd64.tar.gz", "os": "linux", "arch": "amd64", "kind": "archive", "sha256": "1111111111111111111111111111111111111111111111111111111111111111"}
  ]}
]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	_, err := up.Probe(t.Context(), []string{"amd64", "arm64"})
	if err == nil {
		t.Fatal("Probe(missing arch) = nil, want error")
	}
	if !errors.Is(err, ErrUpstreamNotFound) {
		t.Errorf("Probe(missing arch) err = %v, want ErrUpstreamNotFound", err)
	}
	if !strings.Contains(err.Error(), "arm64") {
		t.Errorf("Probe(missing arch) err = %v, want message naming arm64", err)
	}
}

func TestGoUpstream_ProbeNoStableRelease(t *testing.T) {
	body := `[
  {"version": "go1.28rc1", "stable": false, "files": []}
]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	_, err := up.Probe(t.Context(), []string{"amd64"})
	if err == nil {
		t.Fatal("Probe(no stable) = nil, want error")
	}
	if !errors.Is(err, ErrUpstreamNotFound) {
		t.Errorf("Probe(no stable) err = %v, want ErrUpstreamNotFound", err)
	}
}

func TestGoUpstream_ProbeEmptyList(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	up := NewGoUpstream(newTestClient(t, server), server.URL+"/dl/")
	if _, err := up.Probe(t.Context(), []string{"amd64"}); err == nil {
		t.Error("Probe(empty list) = nil, want error")
	}
}
