// Package store provides SQLite persistence for workflow state and audit events.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/approval"
	"github.com/mrprewsh/mcp-deployment-controller/internal/deployment"
	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/pipeops"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a workflow record does not exist.
var ErrNotFound = errors.New("deployment not found")

// SQLite is a SQLite-backed implementation of deployment.Store.
type SQLite struct {
	db *sql.DB
}

// Open creates a SQLite database and applies the Milestone 2 schema.
func Open(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("SQLite path must not be empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the SQLite connection.
func (store *SQLite) Close() error { return store.db.Close() }

// Create atomically writes a queued deployment and its first audit event.
func (store *SQLite) Create(ctx context.Context, deploymentRecord deployment.Deployment, event deployment.Event) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deployments (id, application_name, repository, environment, status, failure_at, error, created_at, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
		deploymentRecord.ID, deploymentRecord.ApplicationName, deploymentRecord.Repository, deploymentRecord.Environment,
		string(deploymentRecord.Status), string(deploymentRecord.FailureAt), deploymentRecord.Error, deploymentRecord.CreatedAt.UnixNano()); err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

// Transition compares the current state, updates it, and appends an event in one transaction.
func (store *SQLite) Transition(ctx context.Context, id string, before, after deployment.Status, deploymentError string, event deployment.Event) (deployment.Deployment, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return deployment.Deployment{}, err
	}
	defer tx.Rollback()

	terminalAt := int64(0)
	if after == deployment.StatusHealthy || after == deployment.StatusFailed {
		terminalAt = event.OccurredAt.UnixNano()
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deployments
		SET status = ?, error = ?,
			started_at = CASE WHEN started_at IS NULL AND ? = 'DEPLOYING' THEN ? ELSE started_at END,
			completed_at = CASE WHEN ? > 0 THEN ? ELSE completed_at END
		WHERE id = ? AND status = ?`,
		string(after), deploymentError, string(after), event.OccurredAt.UnixNano(), terminalAt, terminalAt, id, string(before))
	if err != nil {
		return deployment.Deployment{}, fmt.Errorf("update deployment: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return deployment.Deployment{}, err
	}
	if changed != 1 {
		return deployment.Deployment{}, fmt.Errorf("transition %s from %s to %s: %w", id, before, after, ErrNotFound)
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return deployment.Deployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return deployment.Deployment{}, err
	}
	return store.Get(ctx, id)
}

// Get loads durable workflow state.
func (store *SQLite) Get(ctx context.Context, id string) (deployment.Deployment, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, application_name, repository, environment, status, failure_at, error, created_at, started_at, completed_at
		FROM deployments WHERE id = ?`, id)
	return scanDeployment(row)
}

// ListEvents returns the append-only timeline in insertion order.
func (store *SQLite) ListEvents(ctx context.Context, deploymentID string) ([]deployment.Event, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT event_id, deployment_id, event_type, occurred_at, source, tool_name, arguments_hash, result_summary,
		       protocol_version, status_before, status_after, metadata_json
		FROM deployment_events WHERE deployment_id = ? ORDER BY sequence ASC`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []deployment.Event
	for rows.Next() {
		var event deployment.Event
		var occurredAt int64
		var metadata string
		if err := rows.Scan(&event.ID, &event.DeploymentID, &event.EventType, &occurredAt, &event.Source, &event.ToolName,
			&event.ArgumentsHash, &event.ResultSummary, &event.ProtocolVersion, &event.StatusBefore, &event.StatusAfter, &metadata); err != nil {
			return nil, err
		}
		event.OccurredAt = time.Unix(0, occurredAt).UTC()
		if err := json.Unmarshal([]byte(metadata), &event.Metadata); err != nil {
			return nil, fmt.Errorf("decode event metadata: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// SavePipeOpsDiscovery records the negotiated protocol and the discovered tool names.
func (store *SQLite) SavePipeOpsDiscovery(ctx context.Context, catalog pipeops.Catalog) (string, error) {
	id := "disc_" + fmt.Sprintf("%d", time.Now().UnixNano())
	tools, err := json.Marshal(catalog.Tools)
	if err != nil {
		return "", err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeops_discoveries (id, endpoint, observed_at, protocol_version, tools_json)
		VALUES (?, ?, ?, ?, ?)`, id, catalog.Endpoint, time.Now().UTC().UnixNano(), catalog.ProtocolVersion, string(tools))
	if err != nil {
		return "", err
	}
	return id, nil
}

// SavePlan persists the complete read-only plan as immutable JSON evidence.
func (store *SQLite) SavePlan(ctx context.Context, plan planning.Plan, discoveryID string) error {
	payload, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO deployment_plans (id, discovery_id, created_at, status, provider, plan_json)
		VALUES (?, ?, ?, ?, ?, ?)`, plan.ID, discoveryID, plan.CreatedAt.UnixNano(), plan.Status, plan.Provider, string(payload))
	return err
}

// GetPlan loads and validates immutable plan evidence before it is approved or executed.
func (store *SQLite) GetPlan(ctx context.Context, id string) (planning.Plan, error) {
	var payload string
	if err := store.db.QueryRowContext(ctx, `SELECT plan_json FROM deployment_plans WHERE id = ?`, id).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return planning.Plan{}, ErrNotFound
		}
		return planning.Plan{}, err
	}
	var plan planning.Plan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		return planning.Plan{}, fmt.Errorf("decode deployment plan: %w", err)
	}
	if err := planning.VerifyHash(plan); err != nil {
		return planning.Plan{}, err
	}
	return plan, nil
}

func (store *SQLite) SaveApproval(ctx context.Context, approval approval.Approval) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO deployment_approvals (id, plan_id, plan_hash, decision, actor, created_at) VALUES (?, ?, ?, ?, ?, ?)`, approval.ID, approval.PlanID, approval.PlanHash, string(approval.Decision), approval.Actor, approval.CreatedAt.UnixNano())
	return err
}

func (store *SQLite) GetApproval(ctx context.Context, planID string) (approval.Approval, error) {
	var result approval.Approval
	var created int64
	if err := store.db.QueryRowContext(ctx, `SELECT id, plan_id, plan_hash, decision, actor, created_at FROM deployment_approvals WHERE plan_id = ?`, planID).Scan(&result.ID, &result.PlanID, &result.PlanHash, &result.Decision, &result.Actor, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return approval.Approval{}, ErrNotFound
		}
		return approval.Approval{}, err
	}
	result.CreatedAt = time.Unix(0, created).UTC()
	return result, nil
}

func (store *SQLite) GetExecutionByPlan(ctx context.Context, planID string) (execution.Execution, error) {
	return store.getExecution(ctx, `SELECT id, plan_id, plan_hash, project_id, status, error, created_at, completed_at FROM deployment_executions WHERE plan_id = ?`, planID)
}
func (store *SQLite) GetExecution(ctx context.Context, id string) (execution.Execution, error) {
	return store.getExecution(ctx, `SELECT id, plan_id, plan_hash, project_id, status, error, created_at, completed_at FROM deployment_executions WHERE id = ?`, id)
}
func (store *SQLite) ListExecutionEvents(ctx context.Context, executionID string) ([]execution.Event, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT event_id,execution_id,event_type,tool_name,summary,occurred_at,status_before,status_after FROM deployment_execution_events WHERE execution_id=? ORDER BY sequence`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []execution.Event
	for rows.Next() {
		var event execution.Event
		var occurred int64
		if err := rows.Scan(&event.ID, &event.ExecutionID, &event.EventType, &event.ToolName, &event.Summary, &occurred, &event.StatusBefore, &event.StatusAfter); err != nil {
			return nil, err
		}
		event.OccurredAt = time.Unix(0, occurred).UTC()
		events = append(events, event)
	}
	return events, rows.Err()
}

func (store *SQLite) CreateExecution(ctx context.Context, value execution.Execution, first execution.Event) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_executions (id, plan_id, plan_hash, project_id, status, error, created_at, completed_at) VALUES (?, ?, ?, '', ?, '', ?, NULL)`, value.ID, value.PlanID, value.PlanHash, string(value.Status), value.CreatedAt.UnixNano()); err != nil {
		return err
	}
	if err = insertExecutionEvent(ctx, tx, first); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *SQLite) SetExecution(ctx context.Context, id string, status execution.Status, projectID, executionError string, event execution.Event) (execution.Execution, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.Execution{}, err
	}
	defer tx.Rollback()
	completed := any(nil)
	if status == execution.StatusHealthy || status == execution.StatusFailed || status == execution.StatusUnknown {
		completed = event.OccurredAt.UnixNano()
	}
	result, err := tx.ExecContext(ctx, `UPDATE deployment_executions SET status = ?, project_id = CASE WHEN ? != '' THEN ? ELSE project_id END, error = ?, completed_at = COALESCE(?, completed_at) WHERE id = ?`, string(status), projectID, projectID, executionError, completed, id)
	if err != nil {
		return execution.Execution{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return execution.Execution{}, ErrNotFound
	}
	if err = insertExecutionEvent(ctx, tx, event); err != nil {
		return execution.Execution{}, err
	}
	if err = tx.Commit(); err != nil {
		return execution.Execution{}, err
	}
	return store.getExecution(ctx, `SELECT id, plan_id, plan_hash, project_id, status, error, created_at, completed_at FROM deployment_executions WHERE id = ?`, id)
}

func (store *SQLite) getExecution(ctx context.Context, query, argument string) (execution.Execution, error) {
	var value execution.Execution
	var created int64
	var completed sql.NullInt64
	if err := store.db.QueryRowContext(ctx, query, argument).Scan(&value.ID, &value.PlanID, &value.PlanHash, &value.ProjectID, &value.Status, &value.Error, &created, &completed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return execution.Execution{}, ErrNotFound
		}
		return execution.Execution{}, err
	}
	value.CreatedAt = time.Unix(0, created).UTC()
	if completed.Valid {
		value.CompletedAt = time.Unix(0, completed.Int64).UTC()
	}
	return value, nil
}

func (store *SQLite) migrate(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			application_name TEXT NOT NULL,
			repository TEXT NOT NULL,
			environment TEXT NOT NULL,
			status TEXT NOT NULL,
			failure_at TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			started_at INTEGER,
			completed_at INTEGER
		);
		CREATE TABLE IF NOT EXISTS deployment_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			deployment_id TEXT NOT NULL REFERENCES deployments(id),
			event_type TEXT NOT NULL,
			occurred_at INTEGER NOT NULL,
			source TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			arguments_hash TEXT NOT NULL DEFAULT '',
			result_summary TEXT NOT NULL,
			protocol_version TEXT NOT NULL DEFAULT '',
			status_before TEXT NOT NULL DEFAULT '',
			status_after TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS deployment_events_deployment_id_sequence
			ON deployment_events (deployment_id, sequence);
		CREATE TRIGGER IF NOT EXISTS deployment_events_append_only_update
		BEFORE UPDATE ON deployment_events BEGIN
			SELECT RAISE(ABORT, 'deployment_events is append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_events_append_only_delete
		BEFORE DELETE ON deployment_events BEGIN
			SELECT RAISE(ABORT, 'deployment_events is append-only');
		END;
		CREATE TABLE IF NOT EXISTS pipeops_discoveries (
			id TEXT PRIMARY KEY,
			endpoint TEXT NOT NULL,
			observed_at INTEGER NOT NULL,
			protocol_version TEXT NOT NULL,
			tools_json TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS deployment_plans (
			id TEXT PRIMARY KEY,
			discovery_id TEXT NOT NULL REFERENCES pipeops_discoveries(id),
			created_at INTEGER NOT NULL,
			status TEXT NOT NULL,
			provider TEXT NOT NULL,
			plan_json TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS deployment_approvals (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL UNIQUE REFERENCES deployment_plans(id),
			plan_hash TEXT NOT NULL,
			decision TEXT NOT NULL CHECK (decision IN ('APPROVED', 'DENIED')),
			actor TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS deployment_executions (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL UNIQUE REFERENCES deployment_plans(id),
			plan_hash TEXT NOT NULL,
			project_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			completed_at INTEGER
		);
		CREATE TABLE IF NOT EXISTS deployment_execution_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			execution_id TEXT NOT NULL REFERENCES deployment_executions(id),
			event_type TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			occurred_at INTEGER NOT NULL,
			status_before TEXT NOT NULL DEFAULT '',
			status_after TEXT NOT NULL DEFAULT ''
		);
		CREATE TRIGGER IF NOT EXISTS pipeops_discoveries_append_only_update
		BEFORE UPDATE ON pipeops_discoveries BEGIN
			SELECT RAISE(ABORT, 'pipeops_discoveries are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS pipeops_discoveries_append_only_delete
		BEFORE DELETE ON pipeops_discoveries BEGIN
			SELECT RAISE(ABORT, 'pipeops_discoveries are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_plans_append_only_update
		BEFORE UPDATE ON deployment_plans BEGIN
			SELECT RAISE(ABORT, 'deployment_plans are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_plans_append_only_delete
		BEFORE DELETE ON deployment_plans BEGIN
			SELECT RAISE(ABORT, 'deployment_plans are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_approvals_append_only_update
		BEFORE UPDATE ON deployment_approvals BEGIN
			SELECT RAISE(ABORT, 'deployment_approvals are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_approvals_append_only_delete
		BEFORE DELETE ON deployment_approvals BEGIN
			SELECT RAISE(ABORT, 'deployment_approvals are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_execution_events_append_only_update
		BEFORE UPDATE ON deployment_execution_events BEGIN
			SELECT RAISE(ABORT, 'deployment_execution_events are append-only');
		END;
		CREATE TRIGGER IF NOT EXISTS deployment_execution_events_append_only_delete
		BEFORE DELETE ON deployment_execution_events BEGIN
			SELECT RAISE(ABORT, 'deployment_execution_events are append-only');
		END;`)
	if err != nil {
		return fmt.Errorf("migrate SQLite schema: %w", err)
	}
	return nil
}

func insertExecutionEvent(ctx context.Context, tx *sql.Tx, event execution.Event) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO deployment_execution_events (event_id, execution_id, event_type, tool_name, summary, occurred_at, status_before, status_after) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.ExecutionID, event.EventType, event.ToolName, event.Summary, event.OccurredAt.UnixNano(), string(event.StatusBefore), string(event.StatusAfter))
	return err
}

func insertEvent(ctx context.Context, tx *sql.Tx, event deployment.Event) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode event metadata: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deployment_events (event_id, deployment_id, event_type, occurred_at, source, tool_name, arguments_hash,
			result_summary, protocol_version, status_before, status_after, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.DeploymentID, event.EventType, event.OccurredAt.UnixNano(), event.Source, event.ToolName,
		event.ArgumentsHash, event.ResultSummary, event.ProtocolVersion, string(event.StatusBefore), string(event.StatusAfter), string(metadata))
	return err
}

func scanDeployment(row *sql.Row) (deployment.Deployment, error) {
	var result deployment.Deployment
	var createdAt int64
	var startedAt, completedAt sql.NullInt64
	var failureAt string
	if err := row.Scan(&result.ID, &result.ApplicationName, &result.Repository, &result.Environment, &result.Status, &failureAt,
		&result.Error, &createdAt, &startedAt, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deployment.Deployment{}, ErrNotFound
		}
		return deployment.Deployment{}, err
	}
	result.FailureAt = deployment.FailurePoint(failureAt)
	result.CreatedAt = time.Unix(0, createdAt).UTC()
	if startedAt.Valid {
		value := time.Unix(0, startedAt.Int64).UTC()
		result.StartedAt = &value
	}
	if completedAt.Valid {
		value := time.Unix(0, completedAt.Int64).UTC()
		result.CompletedAt = &value
	}
	return result, nil
}
