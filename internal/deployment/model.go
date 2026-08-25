// Package deployment contains application workflow state; it is independent
// from MCP transport/session state.
package deployment

import (
	"context"
	"time"
)

// Status is the lifecycle state of a fake deployment workflow.
type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusDeploying Status = "DEPLOYING"
	StatusBuilding  Status = "BUILDING"
	StatusVerifying Status = "VERIFYING"
	StatusHealthy   Status = "HEALTHY"
	StatusFailed    Status = "FAILED"
)

// FailurePoint identifies a deliberately simulated failure, never an
// infrastructure fault. Tasks integration is intentionally out of scope.
type FailurePoint string

const FailureAtBuilding FailurePoint = "BUILDING"

// Deployment is durable application state. It is not an MCP session or task.
type Deployment struct {
	ID              string       `json:"id"`
	ApplicationName string       `json:"application_name"`
	Repository      string       `json:"repository"`
	Environment     string       `json:"environment"`
	Status          Status       `json:"status"`
	FailureAt       FailurePoint `json:"failure_at,omitempty"`
	Error           string       `json:"error,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	StartedAt       *time.Time   `json:"started_at,omitempty"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
}

// Event is append-only evidence of a workflow state transition.
type Event struct {
	ID              string            `json:"id"`
	DeploymentID    string            `json:"deployment_id"`
	EventType       string            `json:"event_type"`
	OccurredAt      time.Time         `json:"occurred_at"`
	Source          string            `json:"source"`
	ToolName        string            `json:"tool_name,omitempty"`
	ArgumentsHash   string            `json:"arguments_hash,omitempty"`
	ResultSummary   string            `json:"result_summary"`
	ProtocolVersion string            `json:"protocol_version,omitempty"`
	StatusBefore    Status            `json:"status_before,omitempty"`
	StatusAfter     Status            `json:"status_after,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// CreateInput is the application input required to start a fake deployment.
type CreateInput struct {
	ApplicationName string
	Repository      string
	Environment     string
	FailureAt       FailurePoint
	ProtocolVersion string
	RequestedBy     string
}

// Store is the persistence boundary used by the workflow service.
type Store interface {
	Create(context.Context, Deployment, Event) error
	Transition(context.Context, string, Status, Status, string, Event) (Deployment, error)
	Get(context.Context, string) (Deployment, error)
	ListEvents(context.Context, string) ([]Event, error)
}
