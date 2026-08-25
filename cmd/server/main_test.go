package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

func TestLocalDashboardAndTimelineAPI(t *testing.T) {
	t.Chdir("../..")
	workflowStore, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer workflowStore.Close()
	value := execution.Execution{ID: "exec_e2e", PlanID: "plan_e2e", PlanHash: "hash", Status: execution.StatusHealthy, CreatedAt: time.Now().UTC()}
	// Create the minimal persisted records required by SQLite's execution foreign keys.
	discoveryID, err := workflowStore.SavePipeOpsDiscovery(context.Background(), pipeops.Catalog{Endpoint: "test", ProtocolVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.SavePlan(context.Background(), planning.Plan{ID: "plan_e2e", CreatedAt: time.Now().UTC(), Provider: "pipeops", Status: "PROPOSED"}, discoveryID); err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.CreateExecution(context.Background(), value, execution.Event{ID: "evt_e2e", ExecutionID: value.ID, EventType: "DEPLOYMENT_OBSERVED", Summary: "healthy", OccurredAt: value.CreatedAt, StatusAfter: execution.StatusHealthy}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRootHandler(http.NotFoundHandler(), workflowStore))
	defer server.Close()
	page, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page=%d", page.StatusCode)
	}
	api, err := http.Get(server.URL + "/api/executions/exec_e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer api.Body.Close()
	if api.StatusCode != http.StatusOK {
		t.Fatalf("api=%d", api.StatusCode)
	}
	body, err := io.ReadAll(api.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "DEPLOYMENT_OBSERVED") {
		t.Fatalf("timeline=%s", body)
	}
}
