package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

func TestSQLitePersistsWorkflowAndRejectsAuditEventMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.db")
	createdAt := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.UTC)

	firstStore, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	workflow := deployment.Deployment{
		ID:              "dep_persisted",
		ApplicationName: "fixture",
		Repository:      "example/fixture",
		Environment:     "sandbox",
		Status:          deployment.StatusQueued,
		CreatedAt:       createdAt,
	}
	event := deployment.Event{
		ID:            "evt_persisted",
		DeploymentID:  workflow.ID,
		EventType:     "FAKE_DEPLOYMENT_REQUESTED",
		OccurredAt:    createdAt,
		Source:        "workflow",
		ResultSummary: "Fake deployment workflow queued.",
		StatusAfter:   deployment.StatusQueued,
		Metadata:      map[string]string{"mode": "fake"},
	}
	if err := firstStore.Create(context.Background(), workflow, event); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := firstStore.db.Exec(`UPDATE deployment_events SET result_summary = 'changed' WHERE event_id = ?`, event.ID); err == nil {
		t.Fatal("UPDATE deployment_events error = nil, want append-only trigger rejection")
	}
	if err := firstStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	secondStore, err := Open(path)
	if err != nil {
		t.Fatalf("reopen SQLite database: %v", err)
	}
	defer secondStore.Close()
	persisted, err := secondStore.Get(context.Background(), workflow.ID)
	if err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	}
	if persisted.Status != deployment.StatusQueued || !persisted.CreatedAt.Equal(createdAt) {
		t.Errorf("persisted workflow = %#v, want queued workflow with original timestamp", persisted)
	}
	events, err := secondStore.ListEvents(context.Background(), workflow.ID)
	if err != nil {
		t.Fatalf("ListEvents() after reopen error = %v", err)
	}
	if len(events) != 1 || events[0].ResultSummary != "Fake deployment workflow queued." {
		t.Errorf("persisted events = %#v, want original immutable event", events)
	}
}

func TestSQLitePersistsImmutablePipeOpsDiscoveryAndPlan(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	catalog := pipeops.Catalog{
		Endpoint:        "https://example.test/mcp",
		ProtocolVersion: "2026-07-28",
		Tools:           []pipeops.Tool{{Name: "list_servers"}},
	}
	discoveryID, err := store.SavePipeOpsDiscovery(context.Background(), catalog)
	if err != nil {
		t.Fatalf("SavePipeOpsDiscovery() error = %v", err)
	}
	plan := planning.Plan{
		ID:        "plan_persisted",
		CreatedAt: time.Date(2026, time.August, 25, 11, 0, 0, 0, time.UTC),
		Provider:  "pipeops",
		Status:    "PROPOSED",
		Discovery: catalog,
	}
	plan.Hash, err = planning.Hash(plan)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if err := store.SavePlan(context.Background(), plan, discoveryID); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE pipeops_discoveries SET endpoint = 'changed' WHERE id = ?`, discoveryID); err == nil {
		t.Fatal("UPDATE pipeops_discoveries error = nil, want append-only trigger rejection")
	}
	if _, err := store.db.Exec(`UPDATE deployment_plans SET status = 'APPROVED' WHERE id = ?`, plan.ID); err == nil {
		t.Fatal("UPDATE deployment_plans error = nil, want append-only trigger rejection")
	}

	var endpoint, status string
	if err := store.db.QueryRow(`SELECT endpoint FROM pipeops_discoveries WHERE id = ?`, discoveryID).Scan(&endpoint); err != nil {
		t.Fatalf("read persisted discovery: %v", err)
	}
	if err := store.db.QueryRow(`SELECT status FROM deployment_plans WHERE id = ?`, plan.ID).Scan(&status); err != nil {
		t.Fatalf("read persisted plan: %v", err)
	}
	if endpoint != catalog.Endpoint || status != "PROPOSED" {
		t.Errorf("persisted values = endpoint %q, status %q", endpoint, status)
	}
}

func TestSQLitePersistsApprovalAndExecutionTimeline(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalog := pipeops.Catalog{Endpoint: "https://example.test/mcp", ProtocolVersion: "2026-07-28"}
	discoveryID, err := store.SavePipeOpsDiscovery(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	plan := planning.Plan{ID: "plan_execution", CreatedAt: time.Now().UTC(), Provider: "pipeops", Status: "PROPOSED", Discovery: catalog}
	plan.Hash, err = planning.Hash(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePlan(context.Background(), plan, discoveryID); err != nil {
		t.Fatal(err)
	}
	approval := approval.Approval{ID: "apr_execution", PlanID: plan.ID, PlanHash: plan.Hash, Decision: approval.DecisionApproved, Actor: "tester", CreatedAt: time.Now().UTC()}
	if err := store.SaveApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	exec := execution.Execution{ID: "exec_execution", PlanID: plan.ID, PlanHash: plan.Hash, Status: execution.StatusExecuting, CreatedAt: time.Now().UTC()}
	first := execution.Event{ID: "evt_execution_started", ExecutionID: exec.ID, EventType: "EXECUTION_STARTED", Summary: "started", OccurredAt: exec.CreatedAt, StatusAfter: execution.StatusExecuting}
	if err := store.CreateExecution(context.Background(), exec, first); err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetExecution(context.Background(), exec.ID, execution.StatusHealthy, "project-1", "", execution.Event{ID: "evt_execution_healthy", ExecutionID: exec.ID, EventType: "DEPLOYMENT_OBSERVED", Summary: "healthy", OccurredAt: time.Now().UTC(), StatusBefore: execution.StatusObserving, StatusAfter: execution.StatusHealthy})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != execution.StatusHealthy || updated.ProjectID != "project-1" {
		t.Errorf("execution=%#v", updated)
	}
	if _, err := store.db.Exec(`UPDATE deployment_execution_events SET summary = 'changed' WHERE event_id = 'evt_execution_started'`); err == nil {
		t.Fatal("execution timeline mutation was allowed")
	}
}
