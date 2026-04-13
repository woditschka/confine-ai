package main

import _ "embed"

// Embedded Dockerfiles from samples/ for assistant init and base image build.
// These are compiled into the binary so confine-ai init and confine-ai build-base
// work without access to the source tree.

//go:embed samples/base/Dockerfile
var baseDockerfile []byte

//go:embed samples/claude/.devcontainer/Dockerfile
var claudeDockerfile []byte

//go:embed samples/github-copilot/.devcontainer/Dockerfile
var copilotDockerfile []byte

//go:embed samples/opencode/.devcontainer/Dockerfile
var opencodeDockerfile []byte

// assistantDockerfiles maps known assistant names to their embedded Dockerfiles.
// Used by runInit to select the correct Dockerfile for each assistant.
var assistantDockerfiles = map[string][]byte{
	"claude":   claudeDockerfile,
	"copilot":  copilotDockerfile,
	"opencode": opencodeDockerfile,
}
