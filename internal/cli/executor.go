package cli

import (
	"os/exec"

	"github.com/woditschka/confine-ai/internal/container"
	"github.com/woditschka/confine-ai/internal/runtime"
)

// newExecutor detects the container runtime and returns an executor and the
// runtime metadata. Centralizes the Detect + NewCLIExecutor pattern used by
// every command that talks to the container runtime.
func newExecutor(dockerPath string) (container.Executor, runtime.Runtime, error) {
	rt, err := runtime.Detect(dockerPath, exec.LookPath)
	if err != nil {
		return nil, runtime.Runtime{}, err
	}
	return container.NewCLIExecutor(rt.Path), rt, nil
}
