package recovery

import (
	"testing"

	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

func TestFromFailureRequiresStructuredReadinessEvidence(t *testing.T) {
	plan := planning.Plan{Port: 3000}
	proposal, ok := FromFailure(plan, pipeops.Observation{FailureKind: "READINESS_PROBE_CONNECTION_REFUSED", DetectedPort: 80})
	if !ok || proposal.Current != "3000" || proposal.Proposed != "80" {
		t.Fatalf("proposal=%#v ok=%v", proposal, ok)
	}
	if _, ok := FromFailure(plan, pipeops.Observation{State: "FAILED", Summary: "port 80 may be relevant"}); ok {
		t.Fatal("generic prose created a recovery proposal")
	}
}
