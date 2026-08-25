// Package pipeops is the controller's PipeOps-specific MCP client. It makes
// discovery and read-only validation calls only; it does not call PipeOps HTTP
// APIs directly.
package pipeops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultEndpoint is the official PipeOps remote MCP endpoint.
const DefaultEndpoint = "https://mcp.pipeops.app/mcp"

// Tool is the safe portion of a discovered MCP tool catalog.
type Tool struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ReadOnlyHint    bool   `json:"read_only_hint"`
	IdempotentHint  bool   `json:"idempotent_hint"`
	DestructiveHint *bool  `json:"destructive_hint,omitempty"`
}

// Catalog records a downstream capability discovery result.
type Catalog struct {
	Endpoint        string `json:"endpoint"`
	ProtocolVersion string `json:"protocol_version"`
	Tools           []Tool `json:"tools"`
}

// Target is the read-only validation result for a PipeOps deployment target.
type Target struct {
	WorkspaceID     string `json:"workspace_id"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
	ServerID        string `json:"server_id"`
	ServerName      string `json:"server_name"`
}

// Client calls a remote PipeOps MCP server using a short-lived bearer token.
type Client struct {
	endpoint    string
	accessToken string
	logger      *slog.Logger
}

// Config configures a PipeOps MCP client. AccessToken is intentionally never
// persisted or logged.
type Config struct {
	Endpoint    string
	AccessToken string
	Logger      *slog.Logger
}

// CreateProjectInput is the narrow execution contract supported in Milestone 4.
type CreateProjectInput struct {
	Name, Username, Source, Repository, Branch, BuildMethod string
	Port                                                    int
	WorkspaceID, EnvironmentID, ServerID                    string
}

// Observation is an evidence-based classification, not an assertion that an
// accepted deploy request is already healthy.
type Observation struct{ State, Summary string }

// NewClient validates configuration without making a network request.
func NewClient(config Config) (*Client, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid PipeOps MCP endpoint")
	}
	if strings.TrimSpace(config.AccessToken) == "" {
		return nil, errors.New("PIPEOPS_MCP_ACCESS_TOKEN is not configured")
	}
	return &Client{endpoint: endpoint, accessToken: config.AccessToken, logger: config.Logger}, nil
}

// NewClientFromEnv reads only the runtime configuration required for Milestone 3.
func NewClientFromEnv(logger *slog.Logger) (*Client, error) {
	return NewClient(Config{
		Endpoint:    os.Getenv("PIPEOPS_MCP_ENDPOINT"),
		AccessToken: os.Getenv("PIPEOPS_MCP_ACCESS_TOKEN"),
		Logger:      logger,
	})
}

// Discover lists the downstream tool catalog and records the negotiated version.
func (client *Client) Discover(ctx context.Context) (Catalog, error) {
	session, observed, err := client.connect(ctx)
	if err != nil {
		return Catalog{}, err
	}
	defer session.Close()
	return client.listTools(ctx, session, observed)
}

// DiscoverAndInspectTarget performs only discovery plus PipeOps read tools.
func (client *Client) DiscoverAndInspectTarget(ctx context.Context, workspaceID, environmentID string) (Catalog, Target, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(environmentID) == "" {
		return Catalog{}, Target{}, errors.New("workspace_id and environment_id are required")
	}
	session, observed, err := client.connect(ctx)
	if err != nil {
		return Catalog{}, Target{}, err
	}
	defer session.Close()

	catalog, err := client.listTools(ctx, session, observed)
	if err != nil {
		return Catalog{}, Target{}, err
	}
	if !catalog.hasTool("list_servers") || !catalog.hasTool("list_environments") {
		return Catalog{}, Target{}, errors.New("PipeOps MCP does not expose the read tools required to validate a deployment target")
	}

	servers, err := callJSON(ctx, session, "list_servers", map[string]any{"workspace_id": workspaceID})
	if err != nil {
		return Catalog{}, Target{}, fmt.Errorf("list PipeOps servers: %w", err)
	}
	environments, err := callJSON(ctx, session, "list_environments", map[string]any{"workspace_id": workspaceID})
	if err != nil {
		return Catalog{}, Target{}, fmt.Errorf("list PipeOps environments: %w", err)
	}
	target, err := resolveTarget(workspaceID, environmentID, servers, environments)
	if err != nil {
		return Catalog{}, Target{}, err
	}
	return catalog, target, nil
}

func (client *Client) connect(ctx context.Context) (*mcp.ClientSession, *observingTransport, error) {
	observer := &observingTransport{base: &bearerTransport{token: client.accessToken, base: http.DefaultTransport}}
	httpClient := &http.Client{Transport: observer}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             client.endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-deployment-controller", Version: "0.1.0"}, &mcp.ClientOptions{Logger: client.logger, Capabilities: &mcp.ClientCapabilities{}})
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to PipeOps MCP: %w", err)
	}
	return session, observer, nil
}

func (client *Client) listTools(ctx context.Context, session *mcp.ClientSession, observed *observingTransport) (Catalog, error) {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("list PipeOps MCP tools: %w", err)
	}
	catalog := Catalog{Endpoint: client.endpoint, ProtocolVersion: observed.lastProtocolVersion()}
	for _, tool := range result.Tools {
		entry := Tool{Name: tool.Name, Description: tool.Description}
		if tool.Annotations != nil {
			entry.ReadOnlyHint, entry.IdempotentHint, entry.DestructiveHint = tool.Annotations.ReadOnlyHint, tool.Annotations.IdempotentHint, tool.Annotations.DestructiveHint
		}
		catalog.Tools = append(catalog.Tools, entry)
	}
	if catalog.ProtocolVersion == "" {
		return Catalog{}, errors.New("PipeOps MCP negotiation did not expose a protocol version")
	}
	return catalog, nil
}

// CreateProject invokes the discovered PipeOps create_project write tool.
func (client *Client) CreateProject(ctx context.Context, input CreateProjectInput) (string, error) {
	result, err := client.call(ctx, "create_project", map[string]any{"name": input.Name, "username": input.Username, "source": input.Source, "repository": input.Repository, "branch": input.Branch, "build_method": input.BuildMethod, "port": input.Port, "workspace_id": input.WorkspaceID, "environment_uuid": input.EnvironmentID, "cluster_uuid": input.ServerID})
	if err != nil {
		return "", err
	}
	for _, path := range [][]string{{"data", "project", "uuid"}, {"data", "project", "id"}, {"project", "uuid"}, {"uuid"}, {"id"}} {
		if value := nestedString(result, path...); value != "" {
			return value, nil
		}
	}
	return "", errors.New("PipeOps create_project response did not include a project ID")
}

// DeployProject submits the approved project deployment. It returns after the
// downstream request is accepted; callers must observe separately.
func (client *Client) DeployProject(ctx context.Context, projectID, workspaceID string) error {
	_, err := client.call(ctx, "deploy_project", map[string]any{"project_id": projectID, "workspace_id": workspaceID})
	return err
}

// ObserveProject gathers independent build, project, and runtime-log evidence.
func (client *Client) ObserveProject(ctx context.Context, projectID, workspaceID string) (Observation, error) {
	build, err := client.call(ctx, "get_project_build_logs", map[string]any{"project_id": projectID, "workspace_id": workspaceID, "limit": 200})
	if err != nil {
		return Observation{}, err
	}
	project, err := client.call(ctx, "get_project", map[string]any{"project_id": projectID, "workspace_id": workspaceID})
	if err != nil {
		return Observation{}, err
	}
	logs, err := client.call(ctx, "get_project_logs", map[string]any{"project_id": projectID, "workspace_id": workspaceID, "limit": 200})
	if err != nil {
		return Observation{}, err
	}
	evidence := strings.ToLower(compactJSON(build) + " " + compactJSON(project) + " " + compactJSON(logs))
	if strings.Contains(evidence, "failed") || strings.Contains(evidence, "crash") || strings.Contains(evidence, "error") {
		return Observation{State: "FAILED", Summary: "PipeOps build, project, or runtime evidence reports failure."}, nil
	}
	if strings.Contains(evidence, "healthy") || strings.Contains(evidence, "running") || strings.Contains(evidence, "ready") {
		return Observation{State: "HEALTHY", Summary: "PipeOps build, project, and runtime evidence reports a healthy application."}, nil
	}
	return Observation{State: "OBSERVING", Summary: "Deployment accepted; PipeOps has not yet supplied terminal health evidence."}, nil
}

func (client *Client) call(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	session, _, err := client.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return callJSON(ctx, session, name, arguments)
}

func nestedString(value map[string]any, path ...string) string {
	var current any = value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	text, _ := current.(string)
	return text
}
func compactJSON(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }

func (catalog Catalog) hasTool(name string) bool {
	for _, tool := range catalog.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func callJSON(ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) (map[string]any, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("PipeOps tool %q returned an error", name)
	}
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		return structured, nil
	}
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text.Text), &parsed); err == nil {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("PipeOps tool %q returned no JSON object", name)
}

func resolveTarget(workspaceID, environmentID string, servers, environments map[string]any) (Target, error) {
	environment, ok := findByID(environments, []string{"data", "environments"}, environmentID, []string{"UUID", "uuid", "id"})
	if !ok {
		return Target{}, fmt.Errorf("PipeOps environment %q was not found in workspace %q", environmentID, workspaceID)
	}
	serverID := stringField(environment, "ClusterUUID", "cluster_uuid", "server_id")
	server, ok := findByID(servers, []string{"data", "servers"}, serverID, []string{"uuid", "UUID", "id"})
	if !ok {
		return Target{}, fmt.Errorf("PipeOps server %q for environment %q was not found", serverID, environmentID)
	}
	if status := stringField(server, "status"); status != "" && !strings.EqualFold(status, "available") {
		return Target{}, fmt.Errorf("PipeOps server %q is not available", serverID)
	}
	return Target{
		WorkspaceID:     workspaceID,
		EnvironmentID:   environmentID,
		EnvironmentName: stringField(environment, "Name", "name"),
		ServerID:        serverID,
		ServerName:      stringField(server, "name", "Name"),
	}, nil
}

func findByID(payload map[string]any, path []string, wanted string, idFields []string) (map[string]any, bool) {
	var value any = payload
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value = object[key]
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringField(object, idFields...) == wanted {
			return object, true
		}
	}
	return nil, false
}

func stringField(value map[string]any, names ...string) string {
	for _, name := range names {
		if text, ok := value[name].(string); ok {
			return text
		}
	}
	return ""
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (transport *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

type observingTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	last string
}

func (transport *observingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if version := request.Header.Get("Mcp-Protocol-Version"); version != "" {
		transport.mu.Lock()
		transport.last = version
		transport.mu.Unlock()
	}
	return transport.base.RoundTrip(request)
}

func (transport *observingTransport) lastProtocolVersion() string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.last
}
