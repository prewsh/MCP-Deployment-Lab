package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/mcpserver"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	databasePath := os.Getenv("MCP_DEPLOYMENT_CONTROLLER_DB_PATH")
	if databasePath == "" {
		databasePath = "data/mcp-deployment-controller.db"
	}
	workflowStore, err := store.Open(databasePath)
	if err != nil {
		logger.Error("open workflow database", "path", databasePath, "error", err)
		os.Exit(1)
	}
	defer workflowStore.Close()

	workflows := deployment.NewService(workflowStore, deployment.WithLogger(logger))
	pipeOpsClient, pipeOpsConfigurationError := pipeops.NewClientFromEnv(logger)
	writePolicy := policy.FromEnv()
	approvals := approval.NewService(workflowStore, writePolicy)
	var planner *planning.Service
	var executor *execution.Service
	if pipeOpsConfigurationError != nil {
		logger.Warn("PipeOps planning is disabled", "reason", pipeOpsConfigurationError.Error())
	} else {
		planner = planning.NewService(pipeOpsClient, workflowStore)
		executor = execution.NewService(workflowStore, pipeOpsClient, writePolicy)
	}
	mcpHandler := mcpserver.NewHTTPHandler(logger, workflows, planner, approvals, executor)
	handler := newRootHandler(mcpHandler, workflowStore)

	listenAddress := os.Getenv("MCP_DEPLOYMENT_CONTROLLER_LISTEN_ADDR")
	if listenAddress == "" {
		listenAddress = "127.0.0.1:8080"
	}
	if !strings.HasPrefix(listenAddress, "127.0.0.1:") && os.Getenv("MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND") != "true" {
		logger.Error("network bind is disabled; set MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true only behind authenticated private ingress", "address", listenAddress)
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		logger.Error("listen failed", "address", listenAddress, "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("MCP Deployment Controller started", "address", listener.Addr().String(), "protocol", mcpserver.ProtocolVersion, "database", databasePath, "pipeops_planning_configured", planner != nil)

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM)

	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(listener) }()

	select {
	case signal := <-shutdownSignal:
		logger.Info("shutdown requested", "signal", signal.String())
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

func newRootHandler(mcpHandler http.Handler, workflowStore *store.SQLite) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/executions/") {
			id := strings.TrimPrefix(request.URL.Path, "/api/executions/")
			value, err := workflowStore.GetExecution(request.Context(), id)
			if err != nil {
				http.NotFound(response, request)
				return
			}
			events, err := workflowStore.ListExecutionEvents(request.Context(), id)
			if err != nil {
				http.Error(response, "read timeline", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{"execution": value, "events": events})
			return
		}
		if request.Method == http.MethodGet {
			http.ServeFile(response, request, "web/index.html")
			return
		}
		mcpHandler.ServeHTTP(response, request)
	})
}
