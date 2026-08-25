package planning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
)

func TestCreatePipeOpsPlanPersistsProposalWithoutWriteCalls(t *testing.T) {
	server, writeCalls := planningPipeOpsServer(t)
	defer server.Close()

	client, err := pipeops.NewClient(pipeops.Config{Endpoint: server.URL, AccessToken: "read-only-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	service := NewService(client, store)
	plan, err := service.CreatePipeOpsPlan(context.Background(), Input{
		ApplicationName: "controller-fixture",
		Repository:      "example/controller-fixture",
		Branch:          "main",
		Port:            8080,
		WorkspaceID:     "workspace-1",
		EnvironmentID:   "environment-1",
	})
	if err != nil {
		t.Fatalf("CreatePipeOpsPlan() error = %v", err)
	}
	if plan.Status != "PROPOSED" || plan.Provider != "pipeops" {
		t.Errorf("plan = %#v, want a proposed PipeOps plan", plan)
	}
	if plan.Target.EnvironmentName != "dark-prometheus-beta" || plan.Target.ServerName != "dark-prometheus" {
		t.Errorf("plan target = %#v", plan.Target)
	}
	if len(plan.Operations) != 4 || plan.Operations[2].Tool != "create_project" || !plan.Operations[2].RequiresApproval {
		t.Errorf("plan operations = %#v", plan.Operations)
	}
	if got := writeCalls.Load(); got != 0 {
		t.Errorf("PipeOps write calls = %d, want 0", got)
	}
	if store.discovery.ProtocolVersion == "" || store.plan.ID != plan.ID || store.discoveryID == "" {
		t.Errorf("persisted evidence = %#v", store)
	}
}

func TestCreatePipeOpsPlanRejectsInvalidInputBeforeNetworkCalls(t *testing.T) {
	service := NewService(nil, &memoryStore{})
	_, err := service.CreatePipeOpsPlan(context.Background(), Input{ApplicationName: "fixture", Port: 0})
	if err == nil {
		t.Fatal("CreatePipeOpsPlan() error = nil, want validation error")
	}
}

type memoryStore struct {
	discovery   pipeops.Catalog
	discoveryID string
	plan        Plan
}

func (store *memoryStore) SavePipeOpsDiscovery(_ context.Context, catalog pipeops.Catalog) (string, error) {
	store.discovery = catalog
	store.discoveryID = "disc-test"
	return store.discoveryID, nil
}

func (store *memoryStore) SavePlan(_ context.Context, plan Plan, discoveryID string) error {
	store.plan = plan
	store.discoveryID = discoveryID
	return nil
}

func planningPipeOpsServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	writes := &atomic.Int32{}
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
	for _, name := range []string{"create_project", "deploy_project"} {
		mcp.AddTool(server, &mcp.Tool{Name: name}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			writes.Add(1)
			return nil, map[string]any{"unexpected": true}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer read-only-test-token" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(response, request)
	})), writes
}
