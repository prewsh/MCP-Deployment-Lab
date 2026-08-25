// Package mcpserver exposes the MCP server tools for the current milestone.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/faults"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
)

// ProtocolVersion is the MCP version this server is designed to demonstrate.
const ProtocolVersion = "2026-07-28"

// EchoInput is the input contract for the echo tool.
type EchoInput struct {
	Text string `json:"text" jsonschema:"text to return unchanged"`
}

// EchoOutput is the structured result from the echo tool.
type EchoOutput struct {
	Text string `json:"text"`
}

// InspectRequestOutput contains only safe protocol metadata exposed by the SDK.
type InspectRequestOutput struct {
	ProtocolVersion    string             `json:"protocol_version"`
	Client             *ClientInformation `json:"client,omitempty"`
	ClientCapabilities map[string]any     `json:"client_capabilities,omitempty"`
}

// ClientInformation is a deliberately small, safe representation of MCP client identity.
type ClientInformation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// StartFakeDeploymentInput is intentionally limited to the future deployment request shape.
type StartFakeDeploymentInput struct {
	ApplicationName string `json:"application_name" jsonschema:"name of the application to deploy"`
	Repository      string `json:"repository" jsonschema:"repository reference, such as owner/repository"`
	Environment     string `json:"environment" jsonschema:"target environment, such as sandbox"`
	FailAt          string `json:"fail_at,omitempty" jsonschema:"optional simulated failure point; BUILDING is the only supported value"`
}

// StartFakeDeploymentOutput returns a durable application workflow handle. It
// is not an MCP Task.
type StartFakeDeploymentOutput struct {
	DeploymentID    string `json:"deployment_id"`
	Status          string `json:"status"`
	ApplicationName string `json:"application_name"`
	Repository      string `json:"repository"`
	Environment     string `json:"environment"`
	FailureAt       string `json:"failure_at,omitempty"`
	Message         string `json:"message"`
}

// GetDeploymentInput identifies a durable fake deployment workflow.
type GetDeploymentInput struct {
	DeploymentID string `json:"deployment_id" jsonschema:"deployment handle returned by start_fake_deployment"`
}

// GetDeploymentOutput includes the current workflow state and its append-only timeline.
type GetDeploymentOutput struct {
	Deployment DeploymentOutput `json:"deployment"`
	Events     []EventOutput    `json:"events"`
}

// DeploymentOutput is a safe serialized workflow record.
type DeploymentOutput struct {
	ID              string `json:"id"`
	ApplicationName string `json:"application_name"`
	Repository      string `json:"repository"`
	Environment     string `json:"environment"`
	Status          string `json:"status"`
	FailureAt       string `json:"failure_at,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       string `json:"created_at"`
	StartedAt       string `json:"started_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
}

// EventOutput is a safe serialized append-only audit event.
type EventOutput struct {
	ID              string            `json:"id"`
	EventType       string            `json:"event_type"`
	OccurredAt      string            `json:"occurred_at"`
	Source          string            `json:"source"`
	ToolName        string            `json:"tool_name,omitempty"`
	ArgumentsHash   string            `json:"arguments_hash,omitempty"`
	ResultSummary   string            `json:"result_summary"`
	ProtocolVersion string            `json:"protocol_version,omitempty"`
	StatusBefore    string            `json:"status_before,omitempty"`
	StatusAfter     string            `json:"status_after,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// PlanPipeOpsDeploymentInput is a read-only proposal request. It never calls
// create_project or deploy_project.
type PlanPipeOpsDeploymentInput struct {
	ApplicationName string `json:"application_name" jsonschema:"proposed application name"`
	Repository      string `json:"repository" jsonschema:"repository URL or reference"`
	Branch          string `json:"branch" jsonschema:"repository branch"`
	Port            int    `json:"port" jsonschema:"application port"`
	WorkspaceID     string `json:"workspace_id" jsonschema:"PipeOps workspace UUID"`
	EnvironmentID   string `json:"environment_id" jsonschema:"PipeOps environment UUID"`
}

type DecidePipeOpsPlanInput struct {
	PlanID   string `json:"plan_id"`
	PlanHash string `json:"plan_hash"`
	Decision string `json:"decision" jsonschema:"APPROVED or DENIED"`
	Actor    string `json:"actor" jsonschema:"local approver identity"`
}
type ExecutePipeOpsPlanInput struct {
	PlanID   string `json:"plan_id"`
	PlanHash string `json:"plan_hash"`
	Fault    string `json:"fault,omitempty" jsonschema:"optional experiment: LOST_RESPONSE_AFTER_DEPLOY; enabled only for the approved sandbox"`
}

// NewServer creates the MCP server and registers the completed milestone tools.
func NewServer(logger *slog.Logger, workflows *deployment.Service, planner *planning.Service, approvals *approval.Service, executor *execution.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-deployment-controller",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Instructions: "The controller stores and simulates fake workflows locally. Its PipeOps planning tools are read-only: they discover capabilities and validate targets, but never create or deploy projects.",
		Logger:       logger,
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Title:       "Echo",
		Description: "Returns the supplied text unchanged. Use it to verify tools/call.",
	}, echo)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "inspect_request",
		Title:       "Inspect Request",
		Description: "Returns safe MCP request metadata exposed by the SDK. It never returns headers, tokens, cookies, or request bodies.",
	}, inspectRequest)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_fake_deployment",
		Title:       "Start Fake Deployment",
		Description: "Stores a fake deployment workflow and starts its local simulation. It does not create cloud infrastructure.",
	}, func(ctx context.Context, request *mcp.CallToolRequest, input StartFakeDeploymentInput) (*mcp.CallToolResult, StartFakeDeploymentOutput, error) {
		return startFakeDeployment(ctx, request, workflows, input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_deployment",
		Title:       "Get Deployment",
		Description: "Returns the current local workflow state and append-only audit events for a fake deployment handle.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input GetDeploymentInput) (*mcp.CallToolResult, GetDeploymentOutput, error) {
		return getDeployment(ctx, workflows, input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "discover_pipeops",
		Title:       "Discover PipeOps",
		Description: "Connects to PipeOps MCP, records its negotiated protocol and tool catalog, and does not call infrastructure tools.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return discoverPipeOps(ctx, planner)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "plan_pipeops_deployment",
		Title:       "Plan PipeOps Deployment",
		Description: "Uses only PipeOps discovery, list_servers, and list_environments to validate a target and return a proposal. It never creates or deploys a project.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input PlanPipeOpsDeploymentInput) (*mcp.CallToolResult, planning.Plan, error) {
		return planPipeOpsDeployment(ctx, planner, input)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "decide_pipeops_plan", Title: "Approve or Deny PipeOps Plan", Description: "Records one immutable APPROVED or DENIED decision for an exact plan hash. Approval is subject to the local sandbox policy and does not deploy."}, func(ctx context.Context, _ *mcp.CallToolRequest, input DecidePipeOpsPlanInput) (*mcp.CallToolResult, approval.Approval, error) {
		return decidePipeOpsPlan(ctx, approvals, input)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "execute_pipeops_plan", Title: "Execute Approved PipeOps Plan", Description: "Executes only a matching approved plan inside the local PipeOps sandbox allowlist, then observes until healthy, failed, or unknown."}, func(ctx context.Context, _ *mcp.CallToolRequest, input ExecutePipeOpsPlanInput) (*mcp.CallToolResult, execution.Execution, error) {
		return executePipeOpsPlan(ctx, executor, input)
	})

	return server
}

// NewHTTPHandler creates a localhost-safe, stateless Streamable HTTP MCP handler.
func NewHTTPHandler(logger *slog.Logger, workflows *deployment.Service, planner *planning.Service, approvals *approval.Service, executor *execution.Service) http.Handler {
	server := NewServer(logger, workflows, planner, approvals, executor)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       logger,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})

	return requestLogger(logger, handler)
}

func decidePipeOpsPlan(ctx context.Context, service *approval.Service, input DecidePipeOpsPlanInput) (*mcp.CallToolResult, approval.Approval, error) {
	if service == nil {
		return nil, approval.Approval{}, fmt.Errorf("PipeOps approval is not configured")
	}
	result, err := service.Decide(ctx, input.PlanID, input.PlanHash, approval.Decision(input.Decision), input.Actor)
	return nil, result, err
}
func executePipeOpsPlan(ctx context.Context, service *execution.Service, input ExecutePipeOpsPlanInput) (*mcp.CallToolResult, execution.Execution, error) {
	if service == nil {
		return nil, execution.Execution{}, fmt.Errorf("PipeOps execution is not configured; set a controller PipeOps token and explicit sandbox allowlist")
	}
	result, err := service.ExecuteWithFault(ctx, input.PlanID, input.PlanHash, faults.Mode(input.Fault))
	return nil, result, err
}

func discoverPipeOps(ctx context.Context, planner *planning.Service) (*mcp.CallToolResult, any, error) {
	if planner == nil {
		return nil, nil, fmt.Errorf("PipeOps planning is not configured; set PIPEOPS_MCP_ACCESS_TOKEN before using this tool")
	}
	catalog, discoveryID, err := planner.Discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"discovery_id": discoveryID, "catalog": catalog}, nil
}

func planPipeOpsDeployment(ctx context.Context, planner *planning.Service, input PlanPipeOpsDeploymentInput) (*mcp.CallToolResult, planning.Plan, error) {
	if planner == nil {
		return nil, planning.Plan{}, fmt.Errorf("PipeOps planning is not configured; set PIPEOPS_MCP_ACCESS_TOKEN before using this tool")
	}
	plan, err := planner.CreatePipeOpsPlan(ctx, planning.Input{
		ApplicationName: input.ApplicationName,
		Repository:      input.Repository,
		Branch:          input.Branch,
		Port:            input.Port,
		WorkspaceID:     input.WorkspaceID,
		EnvironmentID:   input.EnvironmentID,
	})
	return nil, plan, err
}

func echo(_ context.Context, _ *mcp.CallToolRequest, input EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
	if strings.TrimSpace(input.Text) == "" {
		return nil, EchoOutput{}, fmt.Errorf("text must not be empty")
	}
	return nil, EchoOutput{Text: input.Text}, nil
}

func inspectRequest(_ context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, InspectRequestOutput, error) {
	output := InspectRequestOutput{ProtocolVersion: request.ProtocolVersion()}
	if client := request.ClientInfo(); client != nil {
		output.Client = &ClientInformation{Name: client.Name, Version: client.Version}
	}
	output.ClientCapabilities = capabilitiesAsMap(request.ClientCapabilities())
	return nil, output, nil
}

func startFakeDeployment(ctx context.Context, request *mcp.CallToolRequest, workflows *deployment.Service, input StartFakeDeploymentInput) (*mcp.CallToolResult, StartFakeDeploymentOutput, error) {
	if workflows == nil {
		return nil, StartFakeDeploymentOutput{}, fmt.Errorf("deployment workflow is not configured")
	}
	requestedBy := ""
	if client := request.ClientInfo(); client != nil {
		requestedBy = client.Name
	}
	workflow, err := workflows.Start(ctx, deployment.CreateInput{
		ApplicationName: input.ApplicationName,
		Repository:      input.Repository,
		Environment:     input.Environment,
		FailureAt:       deployment.FailurePoint(input.FailAt),
		ProtocolVersion: request.ProtocolVersion(),
		RequestedBy:     requestedBy,
	})
	if err != nil {
		return nil, StartFakeDeploymentOutput{}, err
	}
	return nil, StartFakeDeploymentOutput{
		DeploymentID:    workflow.ID,
		Status:          string(workflow.Status),
		ApplicationName: workflow.ApplicationName,
		Repository:      workflow.Repository,
		Environment:     workflow.Environment,
		FailureAt:       string(workflow.FailureAt),
		Message:         "Fake deployment workflow queued. Call get_deployment with deployment_id to observe it.",
	}, nil
}

func getDeployment(ctx context.Context, workflows *deployment.Service, input GetDeploymentInput) (*mcp.CallToolResult, GetDeploymentOutput, error) {
	if workflows == nil {
		return nil, GetDeploymentOutput{}, fmt.Errorf("deployment workflow is not configured")
	}
	workflow, events, err := workflows.Get(ctx, input.DeploymentID)
	if err != nil {
		return nil, GetDeploymentOutput{}, err
	}
	output := GetDeploymentOutput{Deployment: deploymentOutput(workflow), Events: make([]EventOutput, 0, len(events))}
	for _, event := range events {
		output.Events = append(output.Events, eventOutput(event))
	}
	return nil, output, nil
}

func deploymentOutput(workflow deployment.Deployment) DeploymentOutput {
	output := DeploymentOutput{
		ID:              workflow.ID,
		ApplicationName: workflow.ApplicationName,
		Repository:      workflow.Repository,
		Environment:     workflow.Environment,
		Status:          string(workflow.Status),
		FailureAt:       string(workflow.FailureAt),
		Error:           workflow.Error,
		CreatedAt:       workflow.CreatedAt.Format(time.RFC3339Nano),
	}
	if workflow.StartedAt != nil {
		output.StartedAt = workflow.StartedAt.Format(time.RFC3339Nano)
	}
	if workflow.CompletedAt != nil {
		output.CompletedAt = workflow.CompletedAt.Format(time.RFC3339Nano)
	}
	return output
}

func eventOutput(event deployment.Event) EventOutput {
	return EventOutput{
		ID:              event.ID,
		EventType:       event.EventType,
		OccurredAt:      event.OccurredAt.Format(time.RFC3339Nano),
		Source:          event.Source,
		ToolName:        event.ToolName,
		ArgumentsHash:   event.ArgumentsHash,
		ResultSummary:   event.ResultSummary,
		ProtocolVersion: event.ProtocolVersion,
		StatusBefore:    string(event.StatusBefore),
		StatusAfter:     string(event.StatusAfter),
		Metadata:        event.Metadata,
	}
}
