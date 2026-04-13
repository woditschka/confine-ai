// Package update: npm registry adapter.
//
// Authoritative endpoint (see docs/adr/2026-04-12-outbound-http-trust-boundary.md):
//
//	https://registry.npmjs.org/<package>/latest
//	  Returns a JSON document describing the latest published version of
//	  <package>. The adapter reads a single field, `.version`, and returns
//	  it verbatim. The package tarball is never downloaded, and no sha256 is
//	  fetched or verified — REQ-AS-008 is a read-only version equality
//	  check that degrades gracefully on any failure.
//
// The adapter is used by the REQ-AS-008 assistant version gate to decide whether
// the installed claude-code CLI already matches the latest upstream release.
// Every failure mode (network, non-2xx, malformed JSON, missing field) is
// caught by the gate helper and reported as a single stderr warning before
// falling through to the REQ-AS-008 rebuild path.

package update

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// NpmRegistryURL is the default npm public registry base URL. The adapter
// appends `/<package>/latest` to this value.
const NpmRegistryURL = "https://registry.npmjs.org"

// NpmLatestUpstream is a minimal npm registry adapter. It looks up a single
// package via the `/<package>/latest` endpoint and returns the `.version`
// field verbatim.
type NpmLatestUpstream struct {
	client  *Client
	pkg     string
	baseURL string
}

// NewNpmLatestUpstream constructs an adapter backed by the given client. pkg
// is the package name (may be a scoped name such as `@anthropic-ai/claude-code`).
// baseURL should be the registry root without a trailing slash; if empty the
// default NpmRegistryURL is used. Tests pass an httptest.Server URL.
func NewNpmLatestUpstream(client *Client, pkg, baseURL string) *NpmLatestUpstream {
	if baseURL == "" {
		baseURL = NpmRegistryURL
	}
	return &NpmLatestUpstream{
		client:  client,
		pkg:     pkg,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// npmLatestResponse is the minimal subset of the npm `/<package>/latest`
// payload that the adapter relies on. Unknown fields are ignored by
// encoding/json. The `Version` field's JSON type is a string; any other shape
// produces a decode error that the gate helper classifies as a probe failure.
type npmLatestResponse struct {
	Version string `json:"version"`
}

// Probe fetches the latest-version document for the configured package and
// returns its `version` field. A response body missing `version` (or with an
// empty value) is reported as an error wrapping ErrUpstreamNotFound so the
// caller can distinguish metadata gaps from transport failures, though the
// gate helper treats both paths identically (warn and fall through).
func (u *NpmLatestUpstream) Probe(ctx context.Context) (string, error) {
	url := u.baseURL + "/" + u.pkg + "/latest"
	body, err := u.client.Get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("npm upstream: fetch %s: %w", u.pkg, err)
	}
	var decoded npmLatestResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("npm upstream: parse %s: %w", u.pkg, err)
	}
	if decoded.Version == "" {
		return "", fmt.Errorf("npm upstream: %s response missing .version: %w", u.pkg, ErrUpstreamNotFound)
	}
	return decoded.Version, nil
}
