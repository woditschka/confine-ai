package update

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// defaultMaxBodyBytes caps the size of a single response body the update
// client will read into memory. Ten MiB is generous for the JSON metadata and
// plain-text sha256 files the adapters fetch; any response larger than this is
// almost certainly hostile. Defense in depth against a tampered or hijacked
// origin per docs/adr/2026-04-12-outbound-http-trust-boundary.md.
const defaultMaxBodyBytes int64 = 10 * 1024 * 1024

// defaultRequestTimeout is the overall per-request wall-clock budget applied
// to http.Client.Timeout. Probe failures surface as REQ-AS-008 exit code 2;
// sha256 fetch failures surface as exit code 3.
const defaultRequestTimeout = 30 * time.Second

// defaultDialTimeout is the TCP connect timeout applied via net.Dialer.
const defaultDialTimeout = 10 * time.Second

// Sentinel errors returned by Client methods so callers can classify failures
// at the orchestrator layer.
var (
	// ErrInsecureScheme is returned when a caller attempts to fetch a URL
	// whose scheme is not https, including after a 3xx redirect whose
	// Location resolves to an http:// URL. Per the trust-boundary ADR there
	// is no opt-out for plaintext HTTP.
	ErrInsecureScheme = errors.New("update: non-https URL rejected")

	// ErrBodyTooLarge is returned when a response body exceeds the
	// configured body cap. The underlying transport is drained and closed
	// before the error is returned.
	ErrBodyTooLarge = errors.New("update: response body exceeds cap")
)

// Client is the update package's only outbound HTTP client. All upstream
// adapters (Go, Corretto) issue requests through this type so transport
// guarantees (TLS min version, system trust store, proxy, timeouts, body cap,
// User-Agent) are enforced in exactly one place.
//
// Client is safe for concurrent use by multiple goroutines. The adapters
// construct one Client per orchestrator invocation and share it across probe
// and sha256-fetch calls for that run.
type Client struct {
	httpClient   *http.Client
	userAgent    string
	maxBodyBytes int64
}

// NewClient constructs a Client per the trust-boundary ADR: HTTPS-only,
// TLS 1.2 minimum, system trust store, proxy-aware via the stdlib default,
// 10s dial timeout, 30s overall timeout, 10 MiB body cap, and a User-Agent
// of `confine-ai/<version>`. No retries.
func NewClient(version string) *Client {
	if version == "" {
		version = "update"
	}
	transport := &http.Transport{
		// Proxy picks up HTTPS_PROXY / NO_PROXY from the environment.
		// Corporate environments that configure these variables route
		// through their proxy without any confine-ai-specific knowledge.
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        2,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// RootCAs is intentionally nil: the stdlib falls back to the
		// platform trust store. InsecureSkipVerify is never set.
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   defaultRequestTimeout,
		},
		userAgent:    fmt.Sprintf("confine-ai/%s", version),
		maxBodyBytes: defaultMaxBodyBytes,
	}
}

// Get issues an HTTPS GET for rawURL and returns the response body, bounded
// by the client's body cap. Non-2xx responses are errors; the caller does not
// see the body in that case.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	if err := validateHTTPSURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("update client: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update client: %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("update client: %s: http status %d", rawURL, resp.StatusCode)
	}

	// Read up to cap+1 so we can distinguish "exactly cap" from "oversize".
	limited := io.LimitReader(resp.Body, c.maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("update client: read body: %w", err)
	}
	if int64(len(body)) > c.maxBodyBytes {
		return nil, fmt.Errorf("%s: read %d bytes, cap %d: %w", rawURL, len(body), c.maxBodyBytes, ErrBodyTooLarge)
	}
	return body, nil
}

// GetLocation issues an HTTPS GET but does NOT follow redirects; instead it
// returns the absolute Location header of the first 3xx response. Used by the
// Corretto adapter to discover the canonical version from the "latest"
// endpoint without downloading the tarball. The Location URL is validated as
// HTTPS; a redirect to plaintext HTTP is rejected with ErrInsecureScheme.
//
// If the first response is not a 3xx redirect, GetLocation returns an error.
func (c *Client) GetLocation(ctx context.Context, rawURL string) (string, error) {
	if err := validateHTTPSURL(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("update client: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	// Clone the client so we can install a CheckRedirect that stops at the
	// first hop without mutating the shared client.
	stopClient := &http.Client{
		Transport: c.httpClient.Transport,
		Timeout:   c.httpClient.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := stopClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("update client: %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("update client: %s: expected 3xx redirect, got %d", rawURL, resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("update client: %s: %d response missing Location header", rawURL, resp.StatusCode)
	}
	// Resolve Location relative to the request URL so tests serving a
	// relative path via http.Redirect still produce an absolute URL.
	base, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("update client: parse request url: %w", err)
	}
	target, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("update client: parse Location header %q: %w", loc, err)
	}
	resolved := base.ResolveReference(target)
	if resolved.Scheme != "https" {
		return "", fmt.Errorf("location %q resolved to scheme %q: %w", loc, resolved.Scheme, ErrInsecureScheme)
	}
	return resolved.String(), nil
}

// validateHTTPSURL returns ErrInsecureScheme if rawURL is not a parseable
// https:// URL with a host component.
func validateHTTPSURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("update client: parse url %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%q has scheme %q: %w", rawURL, u.Scheme, ErrInsecureScheme)
	}
	if u.Host == "" {
		return fmt.Errorf("update client: url %q has no host", rawURL)
	}
	return nil
}
