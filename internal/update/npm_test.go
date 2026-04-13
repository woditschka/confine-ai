package update

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNpmLatestUpstream_ProbeHappyPath(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/@anthropic-ai/claude-code/latest") {
			t.Errorf("request path = %q, want suffix /@anthropic-ai/claude-code/latest", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "confine-ai/") {
			t.Errorf("User-Agent = %q, want prefix confine-ai/", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@anthropic-ai/claude-code","version":"1.2.3"}`))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	got, err := up.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("Probe version = %q, want 1.2.3", got)
	}
}

func TestNpmLatestUpstream_ProbeServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	if _, err := up.Probe(t.Context()); err == nil {
		t.Fatal("Probe(500) = nil, want error")
	}
}

func TestNpmLatestUpstream_ProbeMalformedJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	if _, err := up.Probe(t.Context()); err == nil {
		t.Fatal("Probe(malformed) = nil, want error")
	}
}

func TestNpmLatestUpstream_ProbeMissingVersionField(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"@anthropic-ai/claude-code"}`))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	_, err := up.Probe(t.Context())
	if err == nil {
		t.Fatal("Probe(missing version) = nil, want error")
	}
	if !errors.Is(err, ErrUpstreamNotFound) {
		t.Errorf("Probe(missing version) err = %v, want ErrUpstreamNotFound", err)
	}
}

func TestNpmLatestUpstream_ProbeEmptyVersionField(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":""}`))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	_, err := up.Probe(t.Context())
	if err == nil {
		t.Fatal("Probe(empty version) = nil, want error")
	}
	if !errors.Is(err, ErrUpstreamNotFound) {
		t.Errorf("Probe(empty version) err = %v, want ErrUpstreamNotFound", err)
	}
}

func TestNpmLatestUpstream_ProbeWrongVersionType(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":123}`))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	if _, err := up.Probe(t.Context()); err == nil {
		t.Fatal("Probe(wrong type) = nil, want error")
	}
}

func TestNpmLatestUpstream_ProbeURLShape(t *testing.T) {
	// Ensure the adapter targets /<pkg>/latest relative to baseURL, even when
	// pkg contains an encoded scope like @anthropic-ai/claude-code.
	var gotURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Path
		_, _ = w.Write([]byte(`{"version":"9.9.9"}`))
	}))
	defer server.Close()

	up := NewNpmLatestUpstream(newTestClient(t, server), "@anthropic-ai/claude-code", server.URL)
	if _, err := up.Probe(t.Context()); err != nil {
		t.Fatalf("Probe unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/@anthropic-ai/claude-code/latest") {
		t.Errorf("requested path = %q, want suffix /@anthropic-ai/claude-code/latest", gotURL)
	}
}
