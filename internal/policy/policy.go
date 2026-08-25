// Package policy applies the controller's local authorization rules.
package policy

import (
	"errors"
	"os"

	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

// Config is deliberately fail-closed: every allowlist value and the explicit
// write switch must be present before a plan can be approved or executed.
type Config struct {
	WriteEnabled         bool
	FaultsEnabled        bool
	AllowedWorkspaceID   string
	AllowedEnvironmentID string
	AllowedServerID      string
}

func FromEnv() Config {
	return Config{
		WriteEnabled:         os.Getenv("PIPEOPS_WRITE_ENABLED") == "true",
		FaultsEnabled:        os.Getenv("ENABLE_FAULT_INJECTION") == "true",
		AllowedWorkspaceID:   os.Getenv("PIPEOPS_ALLOWED_WORKSPACE_ID"),
		AllowedEnvironmentID: os.Getenv("PIPEOPS_ALLOWED_ENVIRONMENT_ID"),
		AllowedServerID:      os.Getenv("PIPEOPS_ALLOWED_SERVER_ID"),
	}
}

// Validate returns an error unless the plan is exactly inside the local
// sandbox allowlist and contains only the M4 create/deploy write sequence.
func Validate(config Config, plan planning.Plan) error {
	if !config.WriteEnabled {
		return errors.New("PipeOps writes are disabled; set PIPEOPS_WRITE_ENABLED=true only for the approved sandbox")
	}
	if config.AllowedWorkspaceID == "" || config.AllowedEnvironmentID == "" || config.AllowedServerID == "" {
		return errors.New("PipeOps sandbox allowlist is incomplete")
	}
	if plan.Provider != "pipeops" || plan.Target.WorkspaceID != config.AllowedWorkspaceID || plan.Target.EnvironmentID != config.AllowedEnvironmentID || plan.Target.ServerID != config.AllowedServerID {
		return errors.New("deployment target is outside the approved PipeOps sandbox")
	}
	if plan.Project.Source != "github" || plan.Project.Username == "" || plan.Project.BuildMethod != "dockerfile" {
		return errors.New("plan is outside the supported GitHub Dockerfile execution policy")
	}
	if len(plan.Operations) < 4 || plan.Operations[2].Tool != "create_project" || plan.Operations[3].Tool != "deploy_project" {
		return errors.New("plan does not contain the required approved write operations")
	}
	for _, name := range []string{"create_project", "deploy_project"} {
		tool, found := catalogTool(plan.Discovery.Tools, name)
		if !found {
			return errors.New("approved write tool is absent from the discovered PipeOps catalog")
		}
		if tool.ReadOnlyHint {
			return errors.New("PipeOps catalog marks an approved write tool read-only; refusing inconsistent execution")
		}
	}
	return nil
}

func catalogTool(tools []pipeops.Tool, name string) (pipeops.Tool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return pipeops.Tool{}, false
}
