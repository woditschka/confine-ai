package update

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient returns a Client whose Transport trusts the test server's
// in-memory TLS roots. Production code never does this — tests depend on
// httptest.NewTLSServer's synthesized certificate.
func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c := NewClient("confine-ai/test")
	// Swap in a transport that trusts the test server's cert but keeps all
	// other Client guarantees (proxy, TLS min, etc.). httptest gives us a
	// pre-configured client; we reuse its Transport.
	tr, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("test server client transport is %T, want *http.Transport", server.Client().Transport)
	}
	// Preserve the MinVersion assertion by composing a new TLS config from
	// the test roots.
	tr.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    tr.TLSClientConfig.RootCAs,
	}
	c.httpClient.Transport = tr
	return c
}

func TestClient_GetHappyPath(t *testing.T) {
	want := []byte("hello world")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "confine-ai/") {
			t.Errorf("User-Agent = %q, want prefix confine-ai/", got)
		}
		_, _ = w.Write(want)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	body, err := c.Get(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("Get(%q) unexpected error: %v", server.URL, err)
	}
	if string(body) != string(want) {
		t.Errorf("Get body = %q, want %q", body, want)
	}
}

func TestClient_RejectsHTTPScheme(t *testing.T) {
	c := NewClient("confine-ai/test")
	_, err := c.Get(t.Context(), "http://example.com/")
	if err == nil {
		t.Fatal("Get(http://) = nil, want error")
	}
	if !errors.Is(err, ErrInsecureScheme) {
		t.Errorf("Get(http://) err = %v, want ErrInsecureScheme", err)
	}
}

func TestClient_RejectsMissingScheme(t *testing.T) {
	c := NewClient("confine-ai/test")
	if _, err := c.Get(t.Context(), "example.com/foo"); err == nil {
		t.Error("Get(no scheme) = nil, want error")
	}
}

func TestClient_NonSuccessStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	_, err := c.Get(t.Context(), server.URL)
	if err == nil {
		t.Fatal("Get(500) = nil, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Get(500) err = %v, want message containing 500", err)
	}
}

func TestClient_BodyCapExceeded(t *testing.T) {
	// Serve a response larger than the configured cap. The client uses a
	// 10 MiB cap in production; tests override it to a smaller value so we
	// do not allocate 10 MiB of fixture bytes.
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'a'
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	c.maxBodyBytes = 1024 // smaller than the response
	_, err := c.Get(t.Context(), server.URL)
	if err == nil {
		t.Fatal("Get(oversize) = nil, want error")
	}
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("Get(oversize) err = %v, want ErrBodyTooLarge", err)
	}
}

func TestClient_Timeout(t *testing.T) {
	// Handler sleeps longer than the configured timeout.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	c.httpClient.Timeout = 50 * time.Millisecond
	_, err := c.Get(t.Context(), server.URL)
	if err == nil {
		t.Fatal("Get(timeout) = nil, want error")
	}
}

func TestClient_GetLocation_FollowsRedirect(t *testing.T) {
	// Corretto's version discovery relies on reading Location from a
	// redirect without following it. The Client must expose a mode that
	// returns the Location header verbatim for 3xx responses.
	targetPath := "/resources/25.0.2.10.1/amazon-corretto-25.0.2.10.1-linux-x64.tar.gz"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest/amazon-corretto-25-x64-linux-jdk.tar.gz") {
			http.Redirect(w, r, targetPath, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	loc, err := c.GetLocation(t.Context(), server.URL+"/downloads/latest/amazon-corretto-25-x64-linux-jdk.tar.gz")
	if err != nil {
		t.Fatalf("GetLocation unexpected error: %v", err)
	}
	if !strings.HasSuffix(loc, targetPath) {
		t.Errorf("GetLocation = %q, want suffix %q", loc, targetPath)
	}
}

func TestClient_GetLocation_RejectsHTTPLocation(t *testing.T) {
	// If the upstream responds with a redirect to an http:// URL we must
	// reject the result rather than silently downgrading TLS.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://example.com/plaintext")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	_, err := c.GetLocation(t.Context(), server.URL)
	if err == nil {
		t.Fatal("GetLocation(http-location) = nil, want error")
	}
	if !errors.Is(err, ErrInsecureScheme) {
		t.Errorf("GetLocation(http-location) err = %v, want ErrInsecureScheme", err)
	}
}

func TestClient_UserAgentIncludesVersion(t *testing.T) {
	var gotUA string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	c.userAgent = "confine-ai/1.2.3"
	if _, err := c.Get(t.Context(), server.URL); err != nil {
		t.Fatalf("Get unexpected error: %v", err)
	}
	if gotUA != "confine-ai/1.2.3" {
		t.Errorf("User-Agent = %q, want confine-ai/1.2.3", gotUA)
	}
}

func TestClient_ContextCancel(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Get(ctx, server.URL); err == nil {
		t.Error("Get(canceled) = nil, want error")
	}
}

// Silence unused imports if any test is skipped.
var _ = io.Discard
