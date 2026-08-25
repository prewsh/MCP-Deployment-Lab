package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/faults"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
)

func TestExecuteRequiresApprovalThenObservesHealthy(t *testing.T) {
	plan := executionPlan(t)
	store := &memoryStore{plan: plan, approval: approval.Approval{PlanID: plan.ID, PlanHash: plan.Hash, Decision: approval.DecisionApproved}}
	client := &fakePipeOps{observation: pipeops.Observation{State: "HEALTHY", Summary: "fixture is healthy"}}
	service := NewService(store, client, policy.Config{WriteEnabled: true, AllowedWorkspaceID: "w", AllowedEnvironmentID: "e", AllowedServerID: "s"}).WithObservationTiming(0, time.Second)
	result, err := service.Execute(context.Background(), plan.ID, plan.Hash)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != StatusHealthy || result.ProjectID != "project-1" {
		t.Errorf("result = %#v", result)
	}
	if client.createCalls != 1 || client.deployCalls != 1 || client.observeCalls != 1 {
		t.Errorf("calls = %#v", client)
	}
	if len(store.events) < 4 {
		t.Errorf("events = %#v", store.events)
	}
	second, err := service.Execute(context.Background(), plan.ID, plan.Hash)
	if err != nil || second.ID != result.ID || client.createCalls != 1 {
		t.Errorf("repeat = %#v, %v; writes=%d", second, err, client.createCalls)
	}
}

func TestLostResponseFaultBecomesUnknownWithoutObservationOrRetry(t *testing.T) {
	plan := executionPlan(t)
	store := &memoryStore{plan: plan, approval: approval.Approval{PlanID: plan.ID, PlanHash: plan.Hash, Decision: approval.DecisionApproved}}
	client := &fakePipeOps{}
	service := NewService(store, client, policy.Config{WriteEnabled: true, FaultsEnabled: true, AllowedWorkspaceID: "w", AllowedEnvironmentID: "e", AllowedServerID: "s"})
	result, err := service.ExecuteWithFault(context.Background(), plan.ID, plan.Hash, faults.LostResponse)
	if err != nil || result.Status != StatusUnknown || client.deployCalls != 1 || client.observeCalls != 0 {
		t.Errorf("result=%#v err=%v calls=%#v", result, err, client)
	}
}

func TestFailedPortEvidenceAddsRecoveryProposalEvent(t *testing.T) {
	plan := executionPlan(t)
	store := &memoryStore{plan: plan, approval: approval.Approval{PlanID: plan.ID, PlanHash: plan.Hash, Decision: approval.DecisionApproved}}
	client := &fakePipeOps{observation: pipeops.Observation{State: "FAILED", Summary: "application listening on port 3000"}}
	service := NewService(store, client, policy.Config{WriteEnabled: true, AllowedWorkspaceID: "w", AllowedEnvironmentID: "e", AllowedServerID: "s"})
	result, err := service.Execute(context.Background(), plan.ID, plan.Hash)
	if err != nil || result.Status != StatusFailed {
		t.Errorf("result=%#v err=%v", result, err)
	}
	found := false
	for _, event := range store.events {
		if event.EventType == "RECOVERY_PROPOSED" {
			found = true
		}
	}
	if !found {
		t.Errorf("events=%#v", store.events)
	}
}
func TestExecuteWithoutApprovalDoesNotCallPipeOps(t *testing.T) {
	plan := executionPlan(t)
	store := &memoryStore{plan: plan}
	client := &fakePipeOps{}
	service := NewService(store, client, policy.Config{WriteEnabled: true, AllowedWorkspaceID: "w", AllowedEnvironmentID: "e", AllowedServerID: "s"})
	if _, err := service.Execute(context.Background(), plan.ID, plan.Hash); err == nil {
		t.Fatal("expected approval error")
	}
	if client.createCalls != 0 {
		t.Fatal("write occurred without approval")
	}
}

type memoryStore struct {
	plan      planning.Plan
	approval  approval.Approval
	execution Execution
	events    []Event
}

func (s *memoryStore) GetPlan(context.Context, string) (planning.Plan, error) { return s.plan, nil }
func (s *memoryStore) GetApproval(context.Context, string) (approval.Approval, error) {
	if s.approval.Decision == "" {
		return approval.Approval{}, errors.New("not found")
	}
	return s.approval, nil
}
func (s *memoryStore) GetExecutionByPlan(context.Context, string) (Execution, error) {
	if s.execution.ID == "" {
		return Execution{}, errors.New("not found")
	}
	return s.execution, nil
}
func (s *memoryStore) CreateExecution(_ context.Context, e Execution, event Event) error {
	s.execution = e
	s.events = append(s.events, event)
	return nil
}
func (s *memoryStore) SetExecution(_ context.Context, _ string, status Status, projectID, errText string, event Event) (Execution, error) {
	s.execution.Status = status
	if projectID != "" {
		s.execution.ProjectID = projectID
	}
	s.execution.Error = errText
	s.events = append(s.events, event)
	return s.execution, nil
}

type fakePipeOps struct {
	createCalls, deployCalls, observeCalls int
	observation                            pipeops.Observation
}

func (f *fakePipeOps) CreateProject(context.Context, pipeops.CreateProjectInput) (string, error) {
	f.createCalls++
	return "project-1", nil
}
func (f *fakePipeOps) DeployProject(context.Context, string, string) error {
	f.deployCalls++
	return nil
}
func (f *fakePipeOps) ObserveProject(context.Context, string, string) (pipeops.Observation, error) {
	f.observeCalls++
	return f.observation, nil
}
func executionPlan(t *testing.T) planning.Plan {
	t.Helper()
	p := planning.Plan{ID: "plan-1", CreatedAt: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), Provider: "pipeops", Status: "PROPOSED", ApplicationName: "fixture", Repository: "owner/fixture", Branch: "main", Port: 8080, Project: planning.ProjectSpec{Source: "github", Username: "owner", BuildMethod: "dockerfile"}, Target: pipeops.Target{WorkspaceID: "w", EnvironmentID: "e", ServerID: "s"}, Discovery: pipeops.Catalog{Tools: []pipeops.Tool{{Name: "create_project"}, {Name: "deploy_project"}}}, Operations: []planning.Operation{{Tool: "list_servers"}, {Tool: "list_environments"}, {Tool: "create_project"}, {Tool: "deploy_project"}}}
	var err error
	p.Hash, err = planning.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
