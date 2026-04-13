// Package gitenv reads the host git user identity and merges it into a
// container environment map. Used by the assistant shortcut for REQ-CL-003
// (host git identity forwarding, honoured unless the --no-git-identity flag
// is passed).
package gitenv
