// Package recovery produces proposals only; it never changes infrastructure.
package recovery

import (
	"fmt"

	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

type Proposal struct {
	Detected, Current, Proposed string
	RequiredActions             []string `json:"required_actions"`
	Risk                        string
}

// FromFailure creates a new proposal only when the observation adapter has
// classified a concrete, sanitized failure. It never parses generic prose or
// performs a change itself.
func FromFailure(plan planning.Plan, observation pipeops.Observation) (Proposal, bool) {
	if observation.FailureKind != "READINESS_PROBE_CONNECTION_REFUSED" || observation.DetectedPort < 1 || observation.DetectedPort > 65535 || observation.DetectedPort == plan.Port {
		return Proposal{}, false
	}
	return Proposal{Detected: "Port mismatch detected from PipeOps readiness-probe evidence.", Current: fmt.Sprintf("%d", plan.Port), Proposed: fmt.Sprintf("%d", observation.DetectedPort), RequiredActions: []string{"update project configuration", "redeploy", "verify health"}, Risk: "MEDIUM"}, true
}
