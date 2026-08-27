package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

func TestLocalDashboardAndTimelineAPI(t *testing.T) {
	t.Chdir("../..")
	workflowStore, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer workflowStore.Close()
	value := execution.Execution{ID: "exec_e2e", PlanID: "plan_e2e", PlanHash: "hash", Status: execution.StatusHealthy, CreatedAt: time.Now().UTC()}
	// Create the minimal persisted records required by SQLite's execution foreign keys.
	discoveryID, err := workflowStore.SavePipeOpsDiscovery(context.Background(), pipeops.Catalog{Endpoint: "test", ProtocolVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.SavePlan(context.Background(), planning.Plan{ID: "plan_e2e", CreatedAt: time.Now().UTC(), Provider: "pipeops", Status: "PROPOSED"}, discoveryID); err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.CreateExecution(context.Background(), value, execution.Event{ID: "evt_e2e", ExecutionID: value.ID, EventType: "DEPLOYMENT_OBSERVED", Summary: "healthy", OccurredAt: value.CreatedAt, StatusAfter: execution.StatusHealthy}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRootHandler(http.NotFoundHandler(), workflowStore, accessConfig{mode: accessModeLoopback}))
	defer server.Close()
	page, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page=%d", page.StatusCode)
	}
	api, err := http.Get(server.URL + "/api/executions/exec_e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer api.Body.Close()
	if api.StatusCode != http.StatusOK {
		t.Fatalf("api=%d", api.StatusCode)
	}
	body, err := io.ReadAll(api.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "DEPLOYMENT_OBSERVED") {
		t.Fatalf("timeline=%s", body)
	}
}

func TestAccessModeMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		address          string
		allowNetworkBind bool
		publicMode       string
		bearerToken      string
		want             accessMode
		wantError        bool
	}{
		{name: "local defaults", address: "127.0.0.1:8080", want: accessModeLoopback},
		{name: "IPv6 loopback defaults", address: "[::1]:8080", want: accessModeLoopback},
		{name: "local authenticated", address: "127.0.0.1:8080", bearerToken: "test-token", want: accessModeAuthenticated},
		{name: "public readonly", address: "0.0.0.0:8080", allowNetworkBind: true, publicMode: "true", want: accessModePublicReadonly},
		{name: "remote authenticated", address: "0.0.0.0:8080", allowNetworkBind: true, bearerToken: "test-token", want: accessModeAuthenticated},
		{name: "remote binding not enabled", address: "0.0.0.0:8080", wantError: true},
		{name: "remote has no access mode", address: "0.0.0.0:8080", allowNetworkBind: true, wantError: true},
		{name: "public and bearer are ambiguous", address: "0.0.0.0:8080", allowNetworkBind: true, publicMode: "true", bearerToken: "test-token", wantError: true},
		{name: "invalid public mode", address: "127.0.0.1:8080", publicMode: "yes", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := newAccessConfig(test.address, test.allowNetworkBind, test.publicMode, test.bearerToken)
			if test.wantError {
				if err == nil {
					t.Fatal("newAccessConfig() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("newAccessConfig() error = %v", err)
			}
			if config.mode != test.want {
				t.Errorf("access mode = %q, want %q", config.mode, test.want)
			}
		})
	}
}

func TestPublicReadonlyModeExposesOnlyPageAndHealth(t *testing.T) {
	t.Chdir("../..")
	workflowStore := testExecutionStore(t)
	var mcpCalls atomic.Int32
	handler := newRootHandler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mcpCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}), workflowStore, accessConfig{mode: accessModePublicReadonly})
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, path := range []string{"/", "/healthz"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			t.Fatalf("GET %s = %d, want 200", path, response.StatusCode)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if path == "/" && (!strings.Contains(string(body), "public read-only mode") || strings.Contains(string(body), "fetch(")) {
			t.Fatalf("public page did not remain static: %s", body)
		}
	}

	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, server.URL+"/api/executions/exec_e2e", ""),
		mustRequest(t, http.MethodPost, server.URL, ""),
	} {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNotFound || len(body) != 0 {
			t.Errorf("%s %s = status %d body %q, want empty 404", request.Method, request.URL.Path, response.StatusCode, body)
		}
	}
	if got := mcpCalls.Load(); got != 0 {
		t.Errorf("MCP handler calls = %d, want 0", got)
	}
}

func TestAuthenticatedModeProtectsMCPAndTimeline(t *testing.T) {
	t.Chdir("../..")
	workflowStore := testExecutionStore(t)
	var mcpCalls atomic.Int32
	handler := newRootHandler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mcpCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}), workflowStore, accessConfig{mode: accessModeAuthenticated, bearerToken: "test-token"})
	server := httptest.NewServer(handler)
	defer server.Close()
	health, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("unauthenticated health status = %d, want 200", health.StatusCode)
	}

	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, server.URL+"/api/executions/exec_e2e", ""),
		mustRequest(t, http.MethodPost, server.URL, "Bearer wrong-token"),
	} {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusUnauthorized || len(body) != 0 {
			t.Errorf("%s %s = status %d body %q, want empty 401", request.Method, request.URL.Path, response.StatusCode, body)
		}
	}

	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, server.URL+"/api/executions/exec_e2e", "Bearer test-token"),
		mustRequest(t, http.MethodPost, server.URL, "Bearer test-token"),
	} {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if request.Method == http.MethodGet && response.StatusCode != http.StatusOK {
			t.Errorf("authenticated timeline status = %d, want 200", response.StatusCode)
		}
		if request.Method == http.MethodPost && response.StatusCode != http.StatusNoContent {
			t.Errorf("authenticated MCP status = %d, want 204", response.StatusCode)
		}
	}
	if got := mcpCalls.Load(); got != 1 {
		t.Errorf("MCP handler calls = %d, want 1", got)
	}
}

func TestLocalTimelineRendersDynamicValuesAsText(t *testing.T) {
	t.Chdir("../..")
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "innerHTML") {
		t.Fatal("timeline page uses innerHTML for execution data")
	}
	if !strings.Contains(string(page), "textContent") {
		t.Fatal("timeline page does not render execution data with textContent")
	}
}

func testExecutionStore(t *testing.T) *store.SQLite {
	t.Helper()
	workflowStore, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflowStore.Close() })
	value := execution.Execution{ID: "exec_e2e", PlanID: "plan_e2e", PlanHash: "hash", Status: execution.StatusHealthy, CreatedAt: time.Now().UTC()}
	discoveryID, err := workflowStore.SavePipeOpsDiscovery(context.Background(), pipeops.Catalog{Endpoint: "test", ProtocolVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.SavePlan(context.Background(), planning.Plan{ID: "plan_e2e", CreatedAt: time.Now().UTC(), Provider: "pipeops", Status: "PROPOSED"}, discoveryID); err != nil {
		t.Fatal(err)
	}
	if err := workflowStore.CreateExecution(context.Background(), value, execution.Event{ID: "evt_e2e", ExecutionID: value.ID, EventType: "DEPLOYMENT_OBSERVED", Summary: "healthy", OccurredAt: value.CreatedAt, StatusAfter: execution.StatusHealthy}); err != nil {
		t.Fatal(err)
	}
	return workflowStore
}

func mustRequest(t *testing.T, method, target, authorization string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	return request
}
