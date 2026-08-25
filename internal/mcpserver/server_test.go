package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

func TestStatelessHTTPServerRegistersToolsAndHandlesCalls(t *testing.T) {
	t.Parallel()

	httpServer := newTestHTTPServer(t)
	defer httpServer.Close()

	session := connectClient(t, httpServer.URL)
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"echo", "inspect_request", "start_fake_deployment", "get_deployment", "discover_pipeops", "plan_pipeops_deployment", "decide_pipeops_plan", "execute_pipeops_plan"} {
		if !names[name] {
			t.Errorf("registered tools = %v; missing %q", names, name)
		}
	}

	echoResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello MCP"},
	})
	if err != nil {
		t.Fatalf("CallTool(echo) error = %v", err)
	}
	if echoResult.IsError {
		t.Fatalf("CallTool(echo) returned an error result: %#v", echoResult.Content)
	}
	if got := structuredValue(t, echoResult)["text"]; got != "hello MCP" {
		t.Errorf("echo text = %v, want %q", got, "hello MCP")
	}

	metadataResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "inspect_request"})
	if err != nil {
		t.Fatalf("CallTool(inspect_request) error = %v", err)
	}
	metadata := structuredValue(t, metadataResult)
	if got := metadata["protocol_version"]; got != ProtocolVersion {
		t.Errorf("inspect_request protocol_version = %v, want %q", got, ProtocolVersion)
	}
	if client, ok := metadata["client"].(map[string]any); !ok || client["name"] != "milestone-1-test-client" {
		t.Errorf("inspect_request client = %#v, want test client identity", metadata["client"])
	}

	fakeResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "start_fake_deployment",
		Arguments: map[string]any{
			"application_name": "fixture",
			"repository":       "example/fixture",
			"environment":      "sandbox",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(start_fake_deployment) error = %v", err)
	}
	fake := structuredValue(t, fakeResult)
	if got := fake["status"]; got != "QUEUED" {
		t.Errorf("fake deployment status = %v, want QUEUED", got)
	}
	deploymentID, _ := fake["deployment_id"].(string)
	if !strings.HasPrefix(deploymentID, "dep_") {
		t.Errorf("fake deployment_id = %q, want dep_ prefix", deploymentID)
	}

	getResult := waitForDeploymentTool(t, session, deploymentID, "HEALTHY")
	get := structuredValue(t, getResult)
	workflow, ok := get["deployment"].(map[string]any)
	if !ok || workflow["status"] != "HEALTHY" {
		t.Errorf("get_deployment result = %#v, want HEALTHY workflow", get)
	}
	events, ok := get["events"].([]any)
	if !ok || len(events) != 5 {
		t.Errorf("get_deployment events = %#v, want five append-only events", get["events"])
	}
}

func TestInvalidToolInputFailsSafely(t *testing.T) {
	t.Parallel()

	httpServer := newTestHTTPServer(t)
	defer httpServer.Close()

	session := connectClient(t, httpServer.URL)
	defer session.Close()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "   "},
	})
	if err != nil {
		t.Fatalf("CallTool(echo invalid input) transport error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("CallTool(echo invalid input) IsError = false, want true")
	}
}

func TestDiscoverAccepts20260728WithoutSession(t *testing.T) {
	t.Parallel()

	httpServer := newTestHTTPServer(t)
	defer httpServer.Close()

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"raw-test-client","version":"v0"}}}}`)
	request, err := http.NewRequest(http.MethodPost, httpServer.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", ProtocolVersion)
	request.Header.Set("Mcp-Method", "server/discover")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("discover request error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("discover status = %d, want 200; body=%s", response.StatusCode, payload)
	}
	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		t.Errorf("Mcp-Session-Id = %q, want empty for stateless server", sessionID)
	}

	var payload struct {
		Result struct {
			SupportedVersions []string `json:"supportedVersions"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	if !contains(payload.Result.SupportedVersions, ProtocolVersion) {
		t.Errorf("discover supportedVersions = %v, want %q", payload.Result.SupportedVersions, ProtocolVersion)
	}
}

func connectClient(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "milestone-1-test-client", Version: "v0.1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v", err)
	}
	return session
}

func newTestHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	workflowStore, err := store.Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatalf("open test workflow store: %v", err)
	}
	t.Cleanup(func() { workflowStore.Close() })
	workflows := deployment.NewService(workflowStore, deployment.WithStepDelay(time.Millisecond), deployment.WithLogger(testLogger()))
	return httptest.NewServer(NewHTTPHandler(testLogger(), workflows, nil, nil, nil))
}

func waitForDeploymentTool(t *testing.T, session *mcp.ClientSession, deploymentID, wantedStatus string) *mcp.CallToolResult {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "get_deployment",
			Arguments: map[string]any{"deployment_id": deploymentID},
		})
		if err != nil {
			t.Fatalf("CallTool(get_deployment) error = %v", err)
		}
		workflow, _ := structuredValue(t, result)["deployment"].(map[string]any)
		if workflow["status"] == wantedStatus {
			return result
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("deployment %q did not reach %s", deploymentID, wantedStatus)
	return nil
}

func structuredValue(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return value
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
