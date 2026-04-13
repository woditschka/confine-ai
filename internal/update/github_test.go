package update

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubReleaseUpstream_Probe(t *testing.T) {
	cases := []struct {
		name        string
		handler     func(http.ResponseWriter, *http.Request)
		wantVersion string
		wantErr     bool
		wantErrIs   error
	}{
		{
			name: "happy path strips v prefix",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":"v0.2.5","name":"v0.2.5"}`))
			},
			wantVersion: "0.2.5",
		},
		{
			name: "tag without v prefix",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":"1.0.0"}`))
			},
			wantVersion: "1.0.0",
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			wantErr: true,
		},
		{
			name: "malformed JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`not json`))
			},
			wantErr: true,
		},
		{
			name: "missing tag_name",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"name":"some release"}`))
			},
			wantErr:   true,
			wantErrIs: ErrUpstreamNotFound,
		},
		{
			name: "empty tag_name",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":""}`))
			},
			wantErr:   true,
			wantErrIs: ErrUpstreamNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(tc.handler))
			t.Cleanup(srv.Close)

			client := newTestClient(t, srv)
			u := NewGitHubReleaseUpstream(client, "org/repo", srv.URL)
			got, err := u.Probe(t.Context())

			if tc.wantErr {
				if err == nil {
					t.Fatalf("Probe() = (%q, nil), want error", got)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Errorf("Probe() err = %v, want %v", err, tc.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Probe() unexpected error: %v", err)
			}
			if got != tc.wantVersion {
				t.Errorf("Probe() = %q, want %q", got, tc.wantVersion)
			}
		})
	}
}

func TestGitHubReleaseUpstream_RequestPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.5"}`))
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv)
	u := NewGitHubReleaseUpstream(client, "opencode-ai/opencode", srv.URL)
	if _, err := u.Probe(t.Context()); err != nil {
		t.Fatalf("Probe() unexpected error: %v", err)
	}
	want := "/repos/opencode-ai/opencode/releases/latest"
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}
