package main

import (
	"context"
	"crypto/subtle"
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

type accessMode string

const (
	accessModeLoopback       accessMode = "loopback"
	accessModePublicReadonly accessMode = "public-readonly"
	accessModeAuthenticated  accessMode = "authenticated"
)

type accessConfig struct {
	mode        accessMode
	bearerToken string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	listenAddress := os.Getenv("MCP_DEPLOYMENT_CONTROLLER_LISTEN_ADDR")
	if listenAddress == "" {
		listenAddress = "127.0.0.1:8080"
	}
	access, err := accessConfigFromEnv(listenAddress)
	if err != nil {
		logger.Error("invalid HTTP access configuration", "error", err)
		os.Exit(1)
	}

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
	handler := newRootHandler(mcpHandler, workflowStore, access)
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		logger.Error("listen failed", "address", listenAddress, "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("MCP Deployment Controller started", "access_mode", access.mode)

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

func accessConfigFromEnv(listenAddress string) (accessConfig, error) {
	return newAccessConfig(
		listenAddress,
		os.Getenv("MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND") == "true",
		os.Getenv("MCP_DEPLOYMENT_CONTROLLER_PUBLIC_MODE"),
		os.Getenv("MCP_DEPLOYMENT_CONTROLLER_MCP_BEARER_TOKEN"),
	)
}

func newAccessConfig(listenAddress string, allowNetworkBind bool, publicModeValue, bearerToken string) (accessConfig, error) {
	publicMode, err := parsePublicMode(publicModeValue)
	if err != nil {
		return accessConfig{}, err
	}
	bearerToken = strings.TrimSpace(bearerToken)
	if publicMode && bearerToken != "" {
		return accessConfig{}, errors.New("public-readonly mode and MCP bearer authentication cannot both be configured")
	}

	loopback, err := isLoopbackAddress(listenAddress)
	if err != nil {
		return accessConfig{}, err
	}
	if !loopback && !allowNetworkBind {
		return accessConfig{}, errors.New("network bind is disabled; set MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true")
	}
	if !loopback && !publicMode && bearerToken == "" {
		return accessConfig{}, errors.New("non-loopback binding requires public-readonly mode or MCP bearer authentication")
	}
	if publicMode {
		return accessConfig{mode: accessModePublicReadonly}, nil
	}
	if bearerToken != "" {
		return accessConfig{mode: accessModeAuthenticated, bearerToken: bearerToken}, nil
	}
	return accessConfig{mode: accessModeLoopback}, nil
}

func parsePublicMode(value string) (bool, error) {
	switch value {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("MCP_DEPLOYMENT_CONTROLLER_PUBLIC_MODE must be true or false")
	}
}

func isLoopbackAddress(listenAddress string) (bool, error) {
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return false, errors.New("MCP_DEPLOYMENT_CONTROLLER_LISTEN_ADDR must include a host and port")
	}
	if host == "localhost" {
		return true, nil
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), nil
}

func newRootHandler(mcpHandler http.Handler, workflowStore *store.SQLite, access accessConfig) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = response.Write([]byte("ok\n"))
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/" {
			if access.mode == accessModePublicReadonly {
				http.ServeFile(response, request, "web/public.html")
				return
			}
			http.ServeFile(response, request, "web/index.html")
			return
		}
		if request.URL.Path == "/api/executions" || strings.HasPrefix(request.URL.Path, "/api/executions/") {
			if access.mode == accessModePublicReadonly || request.Method != http.MethodGet {
				notFoundEmpty(response)
				return
			}
			if access.mode == accessModeAuthenticated && !hasValidBearer(request, access.bearerToken) {
				unauthorized(response)
				return
			}
			id := strings.TrimPrefix(request.URL.Path, "/api/executions/")
			value, err := workflowStore.GetExecution(request.Context(), id)
			if err != nil {
				notFoundEmpty(response)
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
		if access.mode == accessModePublicReadonly {
			notFoundEmpty(response)
			return
		}
		if access.mode == accessModeAuthenticated && !hasValidBearer(request, access.bearerToken) {
			unauthorized(response)
			return
		}
		mcpHandler.ServeHTTP(response, request)
	})
}

func hasValidBearer(request *http.Request, token string) bool {
	provided := request.Header.Get("Authorization")
	expected := "Bearer " + token
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func unauthorized(response http.ResponseWriter) {
	response.WriteHeader(http.StatusUnauthorized)
}

func notFoundEmpty(response http.ResponseWriter) {
	response.WriteHeader(http.StatusNotFound)
}
