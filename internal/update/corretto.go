// Corretto upstream adapter.
//
// Authoritative endpoints (see docs/adr/2026-04-12-outbound-http-trust-boundary.md):
//
//	https://corretto.aws/downloads/latest/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz
//	  Returns a 302 whose Location names the canonical versioned archive path,
//	  e.g., /resources/25.0.2.10.1/amazon-corretto-25.0.2.10.1-linux-x64.tar.gz.
//	  We do NOT download the body; we read the Location header to discover the
//	  version. (The sibling /latest_checksum/ endpoint returns a plain-text MD5
//	  and is NOT used — we want sha256 and a redirect, not md5.)
//
//	https://corretto.aws/downloads/latest_sha256/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz
//	  Returns a plain-text response whose first token is the lowercase-hex
//	  sha256 of the latest archive for <major> on <arch>. Some variants append
//	  whitespace and a filename (sha256sum format); the adapter tolerates both.
//
// The Dockerfile builds the archive URL as
//
//	https://corretto.aws/downloads/resources/<version>/amazon-corretto-<version>-linux-<arch>.tar.gz
//
// per samples/base/Dockerfile, so the version we discover here and the sha256
// we write are consistent with the build-time `sha256sum -c` check inside the
// image.
//
// Corretto's arch naming differs from Go's: confine-ai's marker uses
// arch=amd64|arm64 (to match the Go marker scheme), while Corretto URLs use
// x64|aarch64. The adapter translates between the two.
package update

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CorrettoBaseURL is the default endpoint root for Corretto downloads. It
// ends with "/downloads" so the adapter can append "/latest_checksum/..." or
// "/latest_sha256/..." without worrying about trailing slashes.
const CorrettoBaseURL = "https://corretto.aws/downloads"

// ErrInvalidSha256 is returned when an upstream-provided sha256 value does
// not match the expected 64-character lowercase-hex shape. The orchestrator
// maps this to REQ-AS-008 exit code 3 (sha256 verification failure).
var ErrInvalidSha256 = errors.New("update: invalid sha256 value")

// sha256Pattern is the acceptance shape for a verified sha256 hex digest.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// confineToCorrettoArch maps the marker-level arch token to the arch string
// Corretto uses in its filenames.
var confineToCorrettoArch = map[string]string{
	"amd64": "x64",
	"arm64": "aarch64",
}

// CorrettoUpstream is the Corretto release adapter. It discovers the latest
// version for the caller's current major via the `latest_checksum` redirect
// endpoint, then fetches the matching sha256 from `latest_sha256`.
type CorrettoUpstream struct {
	client  *Client
	baseURL string
}

// NewCorrettoUpstream constructs an adapter backed by the given client.
// baseURL should be the downloads root, e.g., "https://corretto.aws/downloads".
// Tests pass an httptest.Server URL + "/downloads".
func NewCorrettoUpstream(client *Client, baseURL string) *CorrettoUpstream {
	if baseURL == "" {
		baseURL = CorrettoBaseURL
	}
	return &CorrettoUpstream{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// Probe discovers the latest Corretto version for the major implied by
// currentVersion and returns a Resolved with the per-arch sha256 map. The
// orchestrator compares the returned version's major to currentVersion's
// major via MajorVersions to decide whether to prompt the user.
//
// currentVersion is used solely to derive the current major. The adapter does
// not enforce that the returned version is newer than currentVersion; that
// decision belongs to the orchestrator.
func (u *CorrettoUpstream) Probe(ctx context.Context, currentVersion string, arches []string) (Resolved, error) {
	currentMajor, err := parseMajor(currentVersion)
	if err != nil {
		return Resolved{}, fmt.Errorf("corretto upstream: current version: %w", err)
	}

	// Use the first arch to probe the version via the latest_checksum
	// redirect. Corretto guarantees all arches publish the same version
	// concurrently, so one probe suffices.
	if len(arches) == 0 {
		return Resolved{}, errors.New("corretto upstream: no arches requested")
	}
	firstConfineArch := arches[0]
	firstCorrettoArch, ok := confineToCorrettoArch[firstConfineArch]
	if !ok {
		return Resolved{}, fmt.Errorf("corretto upstream: unsupported arch %q", firstConfineArch)
	}

	latestURL := fmt.Sprintf("%s/latest/amazon-corretto-%d-%s-linux-jdk.tar.gz",
		u.baseURL, currentMajor, firstCorrettoArch)
	loc, err := u.client.GetLocation(ctx, latestURL)
	if err != nil {
		return Resolved{}, fmt.Errorf("corretto upstream: version probe: %w", err)
	}
	version, err := parseCorrettoVersionFromLocation(loc)
	if err != nil {
		return Resolved{}, fmt.Errorf("corretto upstream: %w", err)
	}

	shas := make(map[string]string, len(arches))
	for _, confineArch := range arches {
		correttoArch, ok := confineToCorrettoArch[confineArch]
		if !ok {
			return Resolved{}, fmt.Errorf("corretto upstream: unsupported arch %q", confineArch)
		}
		shaURL := fmt.Sprintf("%s/latest_sha256/amazon-corretto-%d-%s-linux-jdk.tar.gz",
			u.baseURL, currentMajor, correttoArch)
		body, err := u.client.Get(ctx, shaURL)
		if err != nil {
			return Resolved{}, fmt.Errorf("corretto upstream: sha256 fetch for %s: %w", confineArch, err)
		}
		sum, err := parseCorrettoSha256(body)
		if err != nil {
			return Resolved{}, fmt.Errorf("corretto upstream: %s: %w", confineArch, err)
		}
		shas[confineArch] = sum
	}
	return Resolved{Version: version, Sha256: shas}, nil
}

// parseCorrettoVersionFromLocation extracts the version string from a
// Corretto resource URL like
//
//	.../resources/25.0.2.10.1/amazon-corretto-25.0.2.10.1-linux-x64.tar.gz
//
// The version appears both in the path segment and in the filename; we read
// the path segment because it is simpler to parse.
func parseCorrettoVersionFromLocation(loc string) (string, error) {
	_, after, ok := strings.Cut(loc, "/resources/")
	if !ok {
		return "", fmt.Errorf("latest_checksum Location %q missing /resources/ segment", loc)
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, "/")
	if !ok0 {
		return "", fmt.Errorf("latest_checksum Location %q has no version segment after /resources/", loc)
	}
	version := before0
	if version == "" {
		return "", fmt.Errorf("latest_checksum Location %q has empty version segment", loc)
	}
	if _, err := parseMajor(version); err != nil {
		return "", fmt.Errorf("latest_checksum Location %q version %q: %w", loc, version, err)
	}
	return version, nil
}

// parseCorrettoSha256 normalizes a sha256 response body into a bare 64-char
// lowercase-hex digest. The body may be a single hex token, a hex token
// followed by whitespace and a filename (sha256sum format), or a hex token
// followed by a trailing newline. Any other shape is ErrInvalidSha256.
func parseCorrettoSha256(body []byte) (string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", fmt.Errorf("empty body: %w", ErrInvalidSha256)
	}
	// First whitespace-delimited token is the digest.
	token := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})[0]
	token = strings.ToLower(token)
	if !sha256Pattern.MatchString(token) {
		return "", fmt.Errorf("%q is not 64-char lowercase hex: %w", token, ErrInvalidSha256)
	}
	return token, nil
}

// parseMajor extracts the leading integer major from a dotted version string
// like "25.0.2.10.1". The major must be a non-negative decimal integer.
func parseMajor(version string) (int, error) {
	if version == "" {
		return 0, errors.New("empty version string")
	}
	before, _, ok := strings.Cut(version, ".")
	var head string
	if !ok {
		head = version
	} else {
		head = before
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0, fmt.Errorf("parse major of %q: %w", version, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("major of %q is negative", version)
	}
	return n, nil
}

// MajorVersions returns the integer major versions of oldV and newV. The
// orchestrator uses this to decide whether the Java group requires a
// major-jump prompt (AC-6 through AC-10).
func MajorVersions(oldV, newV string) (int, int, error) {
	om, err := parseMajor(oldV)
	if err != nil {
		return 0, 0, fmt.Errorf("old version: %w", err)
	}
	nm, err := parseMajor(newV)
	if err != nil {
		return 0, 0, fmt.Errorf("new version: %w", err)
	}
	return om, nm, nil
}
