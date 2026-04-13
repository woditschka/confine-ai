package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GoDLURL is the canonical Go release metadata endpoint. The adapter appends
// `?mode=json` to get the machine-readable catalog rather than the HTML page.
// Overridable via NewGoUpstream for tests and for environments that need to
// point at an alternate mirror (none are allowed per the trust-boundary ADR,
// but tests need the injection point).
const GoDLURL = "https://go.dev/dl/"

// ErrUpstreamNotFound is returned by adapters when the upstream response did
// not contain the expected data: no stable release found, no archive for a
// requested arch, etc. The orchestrator maps this to REQ-AS-008 exit code 2
// (probe failure) when observed at probe time.
var ErrUpstreamNotFound = errors.New("update: upstream resource not found")

// GoUpstream is the Go release-catalog adapter. It fetches the JSON manifest
// published at go.dev/dl/?mode=json and extracts the latest stable version's
// sha256 values for each requested arch.
type GoUpstream struct {
	client  *Client
	baseURL string
}

// NewGoUpstream constructs a GoUpstream backed by the given Client. baseURL
// should be the path up to but not including the query string, e.g.
// "https://go.dev/dl/". Tests pass an httptest.Server URL + path.
func NewGoUpstream(client *Client, baseURL string) *GoUpstream {
	if baseURL == "" {
		baseURL = GoDLURL
	}
	return &GoUpstream{client: client, baseURL: baseURL}
}

// goRelease mirrors the subset of the go.dev/dl JSON schema this adapter
// relies on. Unknown fields are ignored by encoding/json.
type goRelease struct {
	Version string   `json:"version"`
	Stable  bool     `json:"stable"`
	Files   []goFile `json:"files"`
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Kind     string `json:"kind"`
	Sha256   string `json:"sha256"`
}

// Probe fetches the Go release catalog, selects the first stable release, and
// returns its version (without the `go` prefix) and the per-arch sha256 map
// for the requested linux archives. A request for an arch whose archive is
// absent returns an error wrapping ErrUpstreamNotFound so the orchestrator
// can distinguish metadata gaps from transport failures.
func (u *GoUpstream) Probe(ctx context.Context, arches []string) (Resolved, error) {
	url := u.baseURL + "?mode=json"
	body, err := u.client.Get(ctx, url)
	if err != nil {
		return Resolved{}, fmt.Errorf("go upstream: fetch catalog: %w", err)
	}
	var releases []goRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return Resolved{}, fmt.Errorf("go upstream: parse catalog: %w", err)
	}

	var chosen *goRelease
	for i := range releases {
		if releases[i].Stable {
			chosen = &releases[i]
			break
		}
	}
	if chosen == nil {
		return Resolved{}, fmt.Errorf("go upstream: no stable release in catalog: %w", ErrUpstreamNotFound)
	}

	// go.dev versions are reported as "go1.27.1"; the Dockerfile ARG
	// carries the bare "1.27.1" so the RUN step can concatenate it.
	version := strings.TrimPrefix(chosen.Version, "go")
	if version == chosen.Version {
		return Resolved{}, fmt.Errorf("go upstream: unexpected version format %q (missing go prefix)", chosen.Version)
	}

	shas := make(map[string]string, len(arches))
	for _, arch := range arches {
		sum, ok := findGoArchiveSha(chosen.Files, arch)
		if !ok {
			return Resolved{}, fmt.Errorf("go upstream: no linux/%s archive entry for version %s: %w", arch, version, ErrUpstreamNotFound)
		}
		shas[arch] = sum
	}
	return Resolved{Version: version, Sha256: shas}, nil
}

// findGoArchiveSha locates the linux/<arch>/archive entry in files and
// returns its sha256. Returns ok=false if no matching entry exists.
func findGoArchiveSha(files []goFile, arch string) (string, bool) {
	for _, f := range files {
		if f.OS == "linux" && f.Arch == arch && f.Kind == "archive" {
			return f.Sha256, true
		}
	}
	return "", false
}
