// Package update: GitHub releases adapter.
//
// Authoritative endpoint:
//
//	https://api.github.com/repos/{owner}/{repo}/releases/latest
//	  Returns a JSON document describing the latest release. The adapter
//	  reads a single field, `.tag_name`, strips the leading `v` prefix,
//	  and returns the bare semver string. No assets are downloaded — this
//	  is a read-only version equality check that degrades gracefully on
//	  any failure.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitHubAPIURL is the default GitHub API base URL.
const GitHubAPIURL = "https://api.github.com"

// GitHubReleaseUpstream is a minimal GitHub releases adapter. It looks up
// the latest release for a repository and returns the tag name with the
// leading `v` stripped.
type GitHubReleaseUpstream struct {
	client  *Client
	repo    string // "owner/repo"
	baseURL string
}

// NewGitHubReleaseUpstream constructs an adapter backed by the given
// client. repo is the "owner/repo" slug (e.g.
// "opencode-ai/opencode"). baseURL should be the API root without a
// trailing slash; if empty the default GitHubAPIURL is used.
func NewGitHubReleaseUpstream(client *Client, repo, baseURL string) *GitHubReleaseUpstream {
	if baseURL == "" {
		baseURL = GitHubAPIURL
	}
	return &GitHubReleaseUpstream{
		client:  client,
		repo:    repo,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// githubReleaseResponse is the minimal subset of the GitHub
// `/repos/{owner}/{repo}/releases/latest` payload.
type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
}

// Probe fetches the latest release for the configured repository and
// returns its tag name with the leading `v` stripped.
func (u *GitHubReleaseUpstream) Probe(ctx context.Context) (string, error) {
	url := u.baseURL + "/repos/" + u.repo + "/releases/latest"
	body, err := u.client.Get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("github upstream: fetch %s: %w", u.repo, err)
	}
	var decoded githubReleaseResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("github upstream: parse %s: %w", u.repo, err)
	}
	if decoded.TagName == "" {
		return "", fmt.Errorf("github upstream: %s response missing .tag_name: %w", u.repo, ErrUpstreamNotFound)
	}
	return strings.TrimPrefix(decoded.TagName, "v"), nil
}
