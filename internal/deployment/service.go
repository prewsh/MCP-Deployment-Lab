package deployment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Service runs the simulated workflow and persists every transition.
type Service struct {
	store     Store
	logger    *slog.Logger
	stepDelay time.Duration
	now       func() time.Time
}

// Option configures a Service for production or deterministic tests.
type Option func(*Service)

// WithStepDelay sets the delay between simulated lifecycle transitions.
func WithStepDelay(delay time.Duration) Option {
	return func(service *Service) { service.stepDelay = delay }
}

// WithClock supplies a clock for deterministic tests.
func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.now = clock }
}

// WithLogger records only workflow identifiers and statuses.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) { service.logger = logger }
}

// NewService creates a workflow service. The default delay makes lifecycle
// changes observable during a local demonstration without making tests slow.
func NewService(store Store, options ...Option) *Service {
	service := &Service{
		store:     store,
		stepDelay: time.Second,
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Start persists a queued deployment before starting a background simulation.
func (service *Service) Start(ctx context.Context, input CreateInput) (Deployment, error) {
	if service.store == nil {
		return Deployment{}, errors.New("deployment store is not configured")
	}
	if err := validateInput(input); err != nil {
		return Deployment{}, err
	}

	id, err := newID("dep")
	if err != nil {
		return Deployment{}, fmt.Errorf("create deployment id: %w", err)
	}
	now := service.now()
	deployment := Deployment{
		ID:              id,
		ApplicationName: strings.TrimSpace(input.ApplicationName),
		Repository:      strings.TrimSpace(input.Repository),
		Environment:     strings.TrimSpace(input.Environment),
		Status:          StatusQueued,
		FailureAt:       input.FailureAt,
		CreatedAt:       now,
	}
	event := Event{
		ID:              mustID("evt"),
		DeploymentID:    deployment.ID,
		EventType:       "FAKE_DEPLOYMENT_REQUESTED",
		OccurredAt:      now,
		Source:          "workflow",
		ToolName:        "start_fake_deployment",
		ArgumentsHash:   hashInput(input),
		ResultSummary:   "Fake deployment workflow queued.",
		ProtocolVersion: input.ProtocolVersion,
		StatusAfter:     StatusQueued,
		Metadata:        map[string]string{"mode": "fake", "requested_by": input.RequestedBy},
	}
	if err := service.store.Create(ctx, deployment, event); err != nil {
		return Deployment{}, fmt.Errorf("persist deployment: %w", err)
	}

	service.log("fake deployment queued", deployment)
	go service.run(deployment.ID, input.FailureAt)
	return deployment, nil
}

// Get returns the current durable workflow state and its append-only timeline.
func (service *Service) Get(ctx context.Context, deploymentID string) (Deployment, []Event, error) {
	if strings.TrimSpace(deploymentID) == "" {
		return Deployment{}, nil, errors.New("deployment_id must not be empty")
	}
	deployment, err := service.store.Get(ctx, deploymentID)
	if err != nil {
		return Deployment{}, nil, err
	}
	events, err := service.store.ListEvents(ctx, deploymentID)
	if err != nil {
		return Deployment{}, nil, err
	}
	return deployment, events, nil
}

func (service *Service) run(deploymentID string, failureAt FailurePoint) {
	for _, next := range []Status{StatusDeploying, StatusBuilding, StatusVerifying, StatusHealthy} {
		if !service.wait() {
			return
		}

		current, err := service.store.Get(context.Background(), deploymentID)
		if err != nil {
			service.logError("load fake deployment", deploymentID, err)
			return
		}
		if current.Status == StatusFailed || current.Status == StatusHealthy {
			return
		}

		if next == StatusVerifying && failureAt == FailureAtBuilding {
			if _, err := service.transition(deploymentID, StatusBuilding, StatusFailed, "SIMULATED_FAILURE", "Simulated failure injected after BUILDING.", "simulated failure at BUILDING"); err != nil {
				service.logError("record simulated failure", deploymentID, err)
			}
			return
		}

		if _, err := service.transition(deploymentID, current.Status, next, "STATUS_CHANGED", "Fake deployment entered "+string(next)+".", ""); err != nil {
			service.logError("transition fake deployment", deploymentID, err)
			return
		}
	}
}

func (service *Service) transition(id string, before, after Status, eventType, summary, deploymentError string) (Deployment, error) {
	event := Event{
		ID:            mustID("evt"),
		DeploymentID:  id,
		EventType:     eventType,
		OccurredAt:    service.now(),
		Source:        "workflow",
		ResultSummary: summary,
		StatusBefore:  before,
		StatusAfter:   after,
		Metadata:      map[string]string{"mode": "fake"},
	}
	updated, err := service.store.Transition(context.Background(), id, before, after, deploymentError, event)
	if err == nil {
		service.log("fake deployment transitioned", updated)
	}
	return updated, err
}

func (service *Service) wait() bool {
	if service.stepDelay <= 0 {
		return true
	}
	time.Sleep(service.stepDelay)
	return true
}

func (service *Service) log(message string, deployment Deployment) {
	if service.logger != nil {
		service.logger.Info(message, "deployment_id", deployment.ID, "status", deployment.Status)
	}
}

func (service *Service) logError(message, deploymentID string, err error) {
	if service.logger != nil {
		service.logger.Error(message, "deployment_id", deploymentID, "error", err)
	}
}

func validateInput(input CreateInput) error {
	if strings.TrimSpace(input.ApplicationName) == "" {
		return errors.New("application_name must not be empty")
	}
	if strings.TrimSpace(input.Repository) == "" {
		return errors.New("repository must not be empty")
	}
	if strings.TrimSpace(input.Environment) == "" {
		return errors.New("environment must not be empty")
	}
	if input.FailureAt != "" && input.FailureAt != FailureAtBuilding {
		return fmt.Errorf("fail_at must be %q when supplied", FailureAtBuilding)
	}
	return nil
}

func hashInput(input CreateInput) string {
	payload, _ := json.Marshal(struct {
		ApplicationName string       `json:"application_name"`
		Repository      string       `json:"repository"`
		Environment     string       `json:"environment"`
		FailureAt       FailurePoint `json:"failure_at,omitempty"`
	}{input.ApplicationName, input.Repository, input.Environment, input.FailureAt})
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

func mustID(prefix string) string {
	id, err := newID(prefix)
	if err != nil {
		return prefix + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id
}
