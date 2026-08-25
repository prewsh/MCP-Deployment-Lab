package deployment_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

func TestFakeWorkflowReachesHealthyAndPersistsAppendOnlyTimeline(t *testing.T) {
	service, _ := newTestService(t)

	created, err := service.Start(context.Background(), deployment.CreateInput{
		ApplicationName: "fixture",
		Repository:      "example/fixture",
		Environment:     "sandbox",
		ProtocolVersion: "2026-07-28",
		RequestedBy:     "workflow-test",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if created.Status != deployment.StatusQueued {
		t.Fatalf("Start() status = %s, want %s", created.Status, deployment.StatusQueued)
	}

	completed, events := waitForTerminal(t, service, created.ID)
	if completed.Status != deployment.StatusHealthy {
		t.Fatalf("completed status = %s, want %s", completed.Status, deployment.StatusHealthy)
	}
	wantTransitions := []deployment.Status{
		deployment.StatusQueued,
		deployment.StatusDeploying,
		deployment.StatusBuilding,
		deployment.StatusVerifying,
		deployment.StatusHealthy,
	}
	if len(events) != len(wantTransitions) {
		t.Fatalf("events = %d, want %d", len(events), len(wantTransitions))
	}
	for index, wanted := range wantTransitions {
		if events[index].StatusAfter != wanted {
			t.Errorf("event %d StatusAfter = %s, want %s", index, events[index].StatusAfter, wanted)
		}
	}
	if events[0].ArgumentsHash == "" || events[0].ProtocolVersion != "2026-07-28" {
		t.Errorf("initial audit event = %#v, want argument hash and protocol version", events[0])
	}

}

func TestFakeWorkflowFailsAtBuilding(t *testing.T) {
	service, _ := newTestService(t)

	created, err := service.Start(context.Background(), deployment.CreateInput{
		ApplicationName: "broken-fixture",
		Repository:      "example/broken-fixture",
		Environment:     "sandbox",
		FailureAt:       deployment.FailureAtBuilding,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	completed, events := waitForTerminal(t, service, created.ID)
	if completed.Status != deployment.StatusFailed {
		t.Fatalf("completed status = %s, want %s", completed.Status, deployment.StatusFailed)
	}
	if completed.Error == "" {
		t.Fatal("failed workflow error is empty")
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	last := events[len(events)-1]
	if last.EventType != "SIMULATED_FAILURE" || last.StatusBefore != deployment.StatusBuilding || last.StatusAfter != deployment.StatusFailed {
		t.Errorf("last event = %#v, want BUILDING -> FAILED simulated failure", last)
	}
}

func TestInvalidFailurePointIsRejected(t *testing.T) {
	service, _ := newTestService(t)
	_, err := service.Start(context.Background(), deployment.CreateInput{
		ApplicationName: "fixture",
		Repository:      "example/fixture",
		Environment:     "sandbox",
		FailureAt:       "DESTROY",
	})
	if err == nil {
		t.Fatal("Start() error = nil, want invalid failure point rejection")
	}
}

func newTestService(t *testing.T) (*deployment.Service, *store.SQLite) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "workflow.db")
	workflowStore, err := store.Open(databasePath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { workflowStore.Close() })
	return deployment.NewService(workflowStore, deployment.WithStepDelay(time.Millisecond)), workflowStore
}

func waitForTerminal(t *testing.T, service *deployment.Service, id string) (deployment.Deployment, []deployment.Event) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		workflow, events, err := service.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if workflow.Status == deployment.StatusHealthy || workflow.Status == deployment.StatusFailed {
			return workflow, events
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("workflow %q did not reach a terminal state", id)
	return deployment.Deployment{}, nil
}
