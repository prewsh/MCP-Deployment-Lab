package pipeops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDiscoverAndInspectTargetUsesOnlyReadTools(t *testing.T) {
	server := fakePipeOpsServer(t, true)
	defer server.Close()

	client, err := NewClient(Config{Endpoint: server.URL, AccessToken: "read-only-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, target, err := client.DiscoverAndInspectTarget(context.Background(), "workspace-1", "environment-1")
	if err != nil {
		t.Fatalf("DiscoverAndInspectTarget() error = %v", err)
	}
	if catalog.ProtocolVersion != "2026-07-28" {
		t.Errorf("protocol = %q, want 2026-07-28", catalog.ProtocolVersion)
	}
	if !catalog.hasTool("list_servers") || !catalog.hasTool("list_environments") {
		t.Errorf("catalog = %#v, missing required read tools", catalog.Tools)
	}
	if target.ServerID != "server-1" || target.EnvironmentID != "environment-1" || target.ServerName != "dark-prometheus" {
		t.Errorf("target = %#v", target)
	}
}

func TestCreateDeployAndObserveProject(t *testing.T) {
	var createCalls, deployCalls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-pipeops-m4", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "create_project"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		createCalls.Add(1)
		return nil, map[string]any{"data": map[string]any{"project": map[string]any{"uuid": "project-1"}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "deploy_project"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		deployCalls.Add(1)
		return nil, map[string]any{"accepted": true}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_project_build_logs"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"status": "build complete"}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_project"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"data": map[string]any{"project": map[string]any{"status": "running"}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_project_logs"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"entries": []any{"ready"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer read-only-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	client, err := NewClient(Config{Endpoint: httpServer.URL, AccessToken: "read-only-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := client.CreateProject(context.Background(), CreateProjectInput{Name: "fixture", Username: "owner", Source: "github", Repository: "owner/fixture", Branch: "main", BuildMethod: "dockerfile", Port: 8080, WorkspaceID: "w", EnvironmentID: "e", ServerID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeployProject(context.Background(), projectID, "w"); err != nil {
		t.Fatal(err)
	}
	observation, err := client.ObserveProject(context.Background(), projectID, "w")
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "project-1" || observation.State != "HEALTHY" || createCalls.Load() != 1 || deployCalls.Load() != 1 {
		t.Errorf("project=%q observation=%#v create=%d deploy=%d", projectID, observation, createCalls.Load(), deployCalls.Load())
	}
}

func TestClassifyObservationPreservesSanitizedWrongPortEvidence(t *testing.T) {
	observation := classifyObservation(`Readiness probe failed: dial tcp 203.0.113.10:3000: connect: connection refused`)
	if observation.State != "FAILED" || observation.FailureKind != "READINESS_PROBE_CONNECTION_REFUSED" || observation.DetectedPort != 3000 {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.Summary != "PipeOps readiness probe could not connect to port 3000." {
		t.Errorf("summary = %q", observation.Summary)
	}
}

func TestCreateProjectMarksMissingIDAsUncertainWrite(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-pipeops", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "create_project"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"accepted": true}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer read-only-test-token" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(response, request)
	}))
	defer httpServer.Close()
	client, err := NewClient(Config{Endpoint: httpServer.URL, AccessToken: "read-only-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateProject(context.Background(), CreateProjectInput{Name: "fixture"})
	if !errors.Is(err, ErrProjectIDMissing) {
		t.Fatalf("CreateProject() error = %v, want ErrProjectIDMissing", err)
	}
}

func TestDiscoverNegotiatesDownToLegacyServer(t *testing.T) {
	server := fakePipeOpsServer(t, false)
	defer server.Close()

	client, err := NewClient(Config{Endpoint: server.URL, AccessToken: "read-only-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if catalog.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocol = %q, want legacy negotiated version", catalog.ProtocolVersion)
	}
}

func TestNewClientRejectsEmptyToken(t *testing.T) {
	if _, err := NewClient(Config{Endpoint: "https://mcp.pipeops.app/mcp"}); err == nil {
		t.Fatal("NewClient() error = nil, want missing token rejection")
	}
}

func fakePipeOpsServer(t *testing.T, stateless bool) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-pipeops", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list_servers"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct {
		WorkspaceID string `json:"workspace_id"`
	}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"data": map[string]any{"servers": []any{map[string]any{"uuid": "server-1", "name": "dark-prometheus", "status": "available"}}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_environments"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct {
		WorkspaceID string `json:"workspace_id"`
	}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"data": map[string]any{"environments": []any{map[string]any{"UUID": "environment-1", "Name": "dark-prometheus-beta", "ClusterUUID": "server-1"}}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "create_project"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"unexpected": true}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "deploy_project"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"unexpected": true}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: stateless, JSONResponse: true})
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer read-only-test-token" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(response, request)
	}))
}
