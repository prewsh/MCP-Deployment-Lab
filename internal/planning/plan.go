// Package planning creates non-mutating PipeOps deployment plans.
package planning

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
)

// Operation is an auditable proposed or completed MCP operation.
type Operation struct {
	Sequence         int    `json:"sequence"`
	Tool             string `json:"tool"`
	Mode             string `json:"mode"`
	Status           string `json:"status"`
	Summary          string `json:"summary"`
	RequiresApproval bool   `json:"requires_approval"`
}

// Plan is an immutable, non-executing deployment proposal.
type Plan struct {
	ID              string          `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	Provider        string          `json:"provider"`
	Status          string          `json:"status"`
	ApplicationName string          `json:"application_name"`
	Repository      string          `json:"repository"`
	Branch          string          `json:"branch"`
	Port            int             `json:"port"`
	Project         ProjectSpec     `json:"project"`
	Target          pipeops.Target  `json:"target"`
	Discovery       pipeops.Catalog `json:"discovery"`
	Operations      []Operation     `json:"operations"`
	Hash            string          `json:"hash"`
}

// ProjectSpec contains the constrained project fields that approval binds to.
// Milestone 4 intentionally supports GitHub repositories with Dockerfiles only.
type ProjectSpec struct {
	Source      string `json:"source"`
	Username    string `json:"username"`
	BuildMethod string `json:"build_method"`
}

// Input contains only the information required for a read-only plan.
type Input struct {
	ApplicationName string
	Repository      string
	Branch          string
	Port            int
	WorkspaceID     string
	EnvironmentID   string
}

// Store persists discovery evidence and generated plans.
type Store interface {
	SavePipeOpsDiscovery(context.Context, pipeops.Catalog) (string, error)
	SavePlan(context.Context, Plan, string) error
}

// Service combines PipeOps MCP discovery with deterministic plan construction.
type Service struct {
	client *pipeops.Client
	store  Store
	now    func() time.Time
}

// NewService creates a non-mutating planner.
func NewService(client *pipeops.Client, store Store) *Service {
	return &Service{client: client, store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Discover records a downstream tool catalog without making infrastructure calls.
func (service *Service) Discover(ctx context.Context) (pipeops.Catalog, string, error) {
	if service.client == nil {
		return pipeops.Catalog{}, "", errors.New("PipeOps MCP client is not configured")
	}
	catalog, err := service.client.Discover(ctx)
	if err != nil {
		return pipeops.Catalog{}, "", err
	}
	if service.store == nil {
		return pipeops.Catalog{}, "", errors.New("planning store is not configured")
	}
	id, err := service.store.SavePipeOpsDiscovery(ctx, catalog)
	if err != nil {
		return pipeops.Catalog{}, "", err
	}
	return catalog, id, nil
}

// CreatePipeOpsPlan discovers downstream capabilities, validates the selected
// target with read-only tools, persists the evidence, and never calls writes.
func (service *Service) CreatePipeOpsPlan(ctx context.Context, input Input) (Plan, error) {
	if err := validateInput(input); err != nil {
		return Plan{}, err
	}
	if service.client == nil || service.store == nil {
		return Plan{}, errors.New("PipeOps planning is not configured")
	}
	catalog, target, err := service.client.DiscoverAndInspectTarget(ctx, input.WorkspaceID, input.EnvironmentID)
	if err != nil {
		return Plan{}, err
	}
	discoveryID, err := service.store.SavePipeOpsDiscovery(ctx, catalog)
	if err != nil {
		return Plan{}, err
	}
	planID, err := newID("plan")
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		ID:              planID,
		CreatedAt:       service.now(),
		Provider:        "pipeops",
		Status:          "PROPOSED",
		ApplicationName: input.ApplicationName,
		Repository:      input.Repository,
		Branch:          input.Branch,
		Port:            input.Port,
		Project:         buildProjectSpec(input),
		Target:          target,
		Discovery:       catalog,
		Operations:      buildOperations(catalog),
	}
	plan.Hash, err = Hash(plan)
	if err != nil {
		return Plan{}, err
	}
	if err := service.store.SavePlan(ctx, plan, discoveryID); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Hash returns the SHA-256 digest that binds approval to this exact proposal.
func Hash(plan Plan) (string, error) {
	plan.Hash = ""
	payload, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode plan for hashing: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// VerifyHash rejects a persisted plan whose execution intent was changed.
func VerifyHash(plan Plan) error {
	expected, err := Hash(plan)
	if err != nil {
		return err
	}
	if plan.Hash == "" || plan.Hash != expected {
		return errors.New("deployment plan hash does not match its contents")
	}
	return nil
}

func buildOperations(catalog pipeops.Catalog) []Operation {
	operations := []Operation{
		{Sequence: 1, Tool: "list_servers", Mode: "READ", Status: "COMPLETED", Summary: "Validated the PipeOps server for the selected environment.", RequiresApproval: false},
		{Sequence: 2, Tool: "list_environments", Mode: "READ", Status: "COMPLETED", Summary: "Validated the selected PipeOps environment.", RequiresApproval: false},
		{Sequence: 3, Tool: "create_project", Mode: "WRITE", Status: "PROPOSED", Summary: "Create the PipeOps project using the approved deployment inputs.", RequiresApproval: true},
	}
	if catalogHas(catalog, "deploy_project") {
		operations = append(operations, Operation{Sequence: 4, Tool: "deploy_project", Mode: "WRITE", Status: "PROPOSED", Summary: "Trigger the PipeOps deployment and begin observation.", RequiresApproval: true})
	}
	return operations
}

func catalogHas(catalog pipeops.Catalog, name string) bool {
	for _, tool := range catalog.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func validateInput(input Input) error {
	if input.ApplicationName == "" || input.Repository == "" || input.Branch == "" || input.WorkspaceID == "" || input.EnvironmentID == "" {
		return errors.New("application_name, repository, branch, workspace_id, and environment_id are required")
	}
	if input.Port < 1 || input.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func buildProjectSpec(input Input) ProjectSpec {
	username := strings.TrimPrefix(strings.TrimSpace(input.Repository), "https://github.com/")
	username = strings.TrimSuffix(username, ".git")
	if slash := strings.IndexByte(username, '/'); slash >= 0 {
		username = username[:slash]
	}
	return ProjectSpec{Source: "github", Username: username, BuildMethod: "dockerfile"}
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate plan id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}
