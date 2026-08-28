package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

func TestRenderExecutionRedactsSensitiveValues(t *testing.T) {
	started := time.Date(2026, time.August, 28, 17, 0, 0, 0, time.UTC)
	output := renderExecution(
		execution.Execution{ID: "exec_example", PlanID: "plan_example", PlanHash: "plan-hash", ProjectID: "project-private-id", Status: execution.StatusFailed, Error: "Authorization: Bearer super-secret", CreatedAt: started, CompletedAt: started.Add(2 * time.Second)},
		planning.Plan{Discovery: pipeops.Catalog{ProtocolVersion: "2026-07-28"}},
		nil,
	)
	if strings.Contains(output, "super-secret") || strings.Contains(output, "project-private-id") {
		t.Fatalf("output leaked sensitive value:\n%s", output)
	}
	if !strings.Contains(output, "[redacted: sensitive value]") || !strings.Contains(output, "Project ID fingerprint") {
		t.Fatalf("output did not show expected safe representation:\n%s", output)
	}
}

func TestRenderListShowsExecutionMetadata(t *testing.T) {
	output := renderList([]execution.Execution{{ID: "exec_one", Status: execution.StatusHealthy, CreatedAt: time.Date(2026, time.August, 28, 17, 0, 0, 0, time.UTC)}})
	if !strings.Contains(output, "exec_one") || !strings.Contains(output, "HEALTHY") {
		t.Fatalf("output = %q", output)
	}
}
