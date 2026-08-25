// Package execution performs an already-authorized PipeOps plan and observes it.
package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/faults"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
	"github.com/mrprewsh/mcp-deployment-controller/internal/recovery"
)

type Status string

const (
	StatusExecuting Status = "EXECUTING"
	StatusObserving Status = "OBSERVING"
	StatusHealthy   Status = "HEALTHY"
	StatusFailed    Status = "FAILED"
	StatusUnknown   Status = "UNKNOWN"
)

type Execution struct {
	ID, PlanID, PlanHash, ProjectID string
	Status                          Status
	Error                           string
	CreatedAt, CompletedAt          time.Time
}
type Event struct {
	ID, ExecutionID, EventType, ToolName, Summary string
	OccurredAt                                    time.Time
	StatusBefore, StatusAfter                     Status
}

type Store interface {
	GetPlan(context.Context, string) (planning.Plan, error)
	GetApproval(context.Context, string) (approval.Approval, error)
	GetExecutionByPlan(context.Context, string) (Execution, error)
	CreateExecution(context.Context, Execution, Event) error
	SetExecution(context.Context, string, Status, string, string, Event) (Execution, error)
}
type PipeOps interface {
	CreateProject(context.Context, pipeops.CreateProjectInput) (string, error)
	DeployProject(context.Context, string, string) error
	ObserveProject(context.Context, string, string) (pipeops.Observation, error)
}
type Service struct {
	store             Store
	pipeops           PipeOps
	config            policy.Config
	now               func() time.Time
	interval, timeout time.Duration
}

func NewService(store Store, client PipeOps, config policy.Config) *Service {
	return &Service{store: store, pipeops: client, config: config, now: func() time.Time { return time.Now().UTC() }, interval: 2 * time.Second, timeout: 2 * time.Minute}
}
func (s *Service) WithObservationTiming(interval, timeout time.Duration) *Service {
	s.interval, s.timeout = interval, timeout
	return s
}

// Execute is idempotent at the controller boundary: once a plan has an
// execution record, it returns that record instead of issuing another write.
func (s *Service) Execute(ctx context.Context, planID, planHash string) (Execution, error) {
	return s.ExecuteWithFault(ctx, planID, planHash, "")
}

// ExecuteWithFault runs the one permitted M5 uncertainty experiment only when
// the caller enables it explicitly and the existing sandbox policy allows it.
func (s *Service) ExecuteWithFault(ctx context.Context, planID, planHash string, fault faults.Mode) (Execution, error) {
	if s.store == nil || s.pipeops == nil {
		return Execution{}, errors.New("execution is not configured")
	}
	if existing, err := s.store.GetExecutionByPlan(ctx, planID); err == nil {
		return existing, nil
	}
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		return Execution{}, err
	}
	if err := planning.VerifyHash(plan); err != nil {
		return Execution{}, err
	}
	if plan.Hash != planHash {
		return Execution{}, errors.New("execution hash does not match the stored plan")
	}
	approval, err := s.store.GetApproval(ctx, planID)
	if err != nil {
		return Execution{}, err
	}
	if approval.Decision != approvalpkgApproved || approval.PlanHash != planHash {
		return Execution{}, errors.New("execution requires a matching approved plan")
	}
	if err := policy.Validate(s.config, plan); err != nil {
		return Execution{}, err
	}
	if err := faults.Validate(s.config, plan, fault); err != nil {
		return Execution{}, err
	}
	id, err := randomID("exec")
	if err != nil {
		return Execution{}, err
	}
	now := s.now()
	exec := Execution{ID: id, PlanID: plan.ID, PlanHash: plan.Hash, Status: StatusExecuting, CreatedAt: now}
	if err := s.store.CreateExecution(ctx, exec, event(id, "EXECUTION_STARTED", "", "Approved plan execution started.", "", StatusExecuting, now)); err != nil {
		return Execution{}, err
	}
	projectID, err := s.pipeops.CreateProject(ctx, pipeops.CreateProjectInput{Name: plan.ApplicationName, Username: plan.Project.Username, Source: plan.Project.Source, Repository: plan.Repository, Branch: plan.Branch, BuildMethod: plan.Project.BuildMethod, Port: plan.Port, WorkspaceID: plan.Target.WorkspaceID, EnvironmentID: plan.Target.EnvironmentID, ServerID: plan.Target.ServerID})
	if err != nil {
		return s.fail(ctx, exec, "create_project failed: "+err.Error())
	}
	exec, err = s.store.SetExecution(ctx, id, StatusExecuting, projectID, "", event(id, "PROJECT_CREATED", "create_project", "PipeOps project created.", StatusExecuting, StatusExecuting, s.now()))
	if err != nil {
		return Execution{}, err
	}
	if err := s.pipeops.DeployProject(ctx, projectID, plan.Target.WorkspaceID); err != nil {
		return s.fail(ctx, exec, "deploy_project failed: "+err.Error())
	}
	if fault == faults.LostResponse {
		return s.store.SetExecution(ctx, id, StatusUnknown, projectID, "", event(id, "DEPLOY_RESPONSE_WITHHELD", "deploy_project", "Fault injected after PipeOps accepted deploy; state is intentionally unknown and no retry was attempted.", StatusExecuting, StatusUnknown, s.now()))
	}
	exec, err = s.store.SetExecution(ctx, id, StatusObserving, projectID, "", event(id, "DEPLOYMENT_ACCEPTED", "deploy_project", "PipeOps accepted deployment; observing real state.", StatusExecuting, StatusObserving, s.now()))
	if err != nil {
		return Execution{}, err
	}
	deadline := s.now().Add(s.timeout)
	for {
		observation, err := s.pipeops.ObserveProject(ctx, projectID, plan.Target.WorkspaceID)
		if err != nil {
			return s.unknown(ctx, exec, "observation failed: "+err.Error())
		}
		if observation.State == "HEALTHY" {
			return s.store.SetExecution(ctx, id, StatusHealthy, projectID, "", event(id, "DEPLOYMENT_OBSERVED", "get_project", observation.Summary, StatusObserving, StatusHealthy, s.now()))
		}
		if observation.State == "FAILED" {
			if proposal, ok := recovery.FromFailure(plan, observation.Summary); ok {
				if _, err := s.store.SetExecution(ctx, id, StatusObserving, projectID, "", event(id, "RECOVERY_PROPOSED", "", proposal.Detected+" Current: "+proposal.Current+"; proposed: "+proposal.Proposed+". Actions: update configuration, redeploy, verify health.", StatusObserving, StatusObserving, s.now())); err != nil {
					return Execution{}, err
				}
			}
			return s.fail(ctx, exec, observation.Summary)
		}
		if !s.now().Before(deadline) {
			return s.unknown(ctx, exec, "observation timed out without terminal health evidence")
		}
		if _, err := s.store.SetExecution(ctx, id, StatusObserving, projectID, "", event(id, "OBSERVATION_PENDING", "get_project", observation.Summary, StatusObserving, StatusObserving, s.now())); err != nil {
			return Execution{}, err
		}
		select {
		case <-ctx.Done():
			return s.unknown(ctx, exec, ctx.Err().Error())
		case <-time.After(s.interval):
		}
	}
}

const approvalpkgApproved = approval.DecisionApproved

func (s *Service) fail(ctx context.Context, exec Execution, message string) (Execution, error) {
	return s.store.SetExecution(ctx, exec.ID, StatusFailed, exec.ProjectID, message, event(exec.ID, "EXECUTION_FAILED", "", message, exec.Status, StatusFailed, s.now()))
}
func (s *Service) unknown(ctx context.Context, exec Execution, message string) (Execution, error) {
	return s.store.SetExecution(ctx, exec.ID, StatusUnknown, exec.ProjectID, message, event(exec.ID, "EXECUTION_UNKNOWN", "", message, exec.Status, StatusUnknown, s.now()))
}
func event(id, kind, tool, summary string, before, after Status, at time.Time) Event {
	return Event{ID: id + "_" + kind + "_" + fmt.Sprint(at.UnixNano()), ExecutionID: id, EventType: kind, ToolName: tool, Summary: summary, OccurredAt: at, StatusBefore: before, StatusAfter: after}
}
func randomID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}
