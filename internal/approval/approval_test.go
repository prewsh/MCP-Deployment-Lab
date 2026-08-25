package approval

import (
	"context"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
)

func TestApproveRequiresExactPlanHashAndSandboxPolicy(t *testing.T) {
	plan := approvedPlan(t)
	store := &testStore{plan: plan}
	service := NewService(store, policy.Config{WriteEnabled: true, AllowedWorkspaceID: "w", AllowedEnvironmentID: "e", AllowedServerID: "s"})
	if _, err := service.Decide(context.Background(), plan.ID, "tampered", DecisionApproved, "tester"); err == nil {
		t.Fatal("approval with wrong hash succeeded")
	}
	if _, err := service.Decide(context.Background(), plan.ID, plan.Hash, DecisionApproved, "tester"); err != nil {
		t.Fatalf("approval error: %v", err)
	}
	if store.approval.PlanHash != plan.Hash || store.approval.Decision != DecisionApproved {
		t.Errorf("approval = %#v", store.approval)
	}
}

type testStore struct {
	plan     planning.Plan
	approval Approval
}

func (s *testStore) GetPlan(context.Context, string) (planning.Plan, error) { return s.plan, nil }
func (s *testStore) SaveApproval(_ context.Context, a Approval) error       { s.approval = a; return nil }
func (s *testStore) GetApproval(context.Context, string) (Approval, error)  { return s.approval, nil }
func approvedPlan(t *testing.T) planning.Plan {
	t.Helper()
	plan := planning.Plan{ID: "plan-1", CreatedAt: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), Provider: "pipeops", Status: "PROPOSED", ApplicationName: "fixture", Repository: "owner/fixture", Branch: "main", Port: 8080, Project: planning.ProjectSpec{Source: "github", Username: "owner", BuildMethod: "dockerfile"}, Target: structTarget(), Discovery: pipeops.Catalog{Tools: []pipeops.Tool{{Name: "create_project"}, {Name: "deploy_project"}}}, Operations: []planning.Operation{{Tool: "list_servers"}, {Tool: "list_environments"}, {Tool: "create_project"}, {Tool: "deploy_project"}}}
	var err error
	plan.Hash, err = planning.Hash(plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func structTarget() pipeops.Target {
	return pipeops.Target{WorkspaceID: "w", EnvironmentID: "e", ServerID: "s"}
}
