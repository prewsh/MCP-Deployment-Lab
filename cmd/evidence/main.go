// Command evidence renders a local SQLite execution timeline as Markdown.
// It never starts an HTTP server and does not read outbound credentials.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/execution"
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/store"
)

const defaultDatabasePath = "data/mcp-deployment-controller.db"

type evidenceStore interface {
	ListExecutions(context.Context) ([]execution.Execution, error)
	GetExecution(context.Context, string) (execution.Execution, error)
	GetPlan(context.Context, string) (planning.Plan, error)
	ListExecutionEvents(context.Context, string) ([]execution.Event, error)
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, func(path string) (evidenceStore, io.Closer, error) {
		database, err := store.Open(path)
		return database, database, err
	}); err != nil {
		fmt.Fprintln(os.Stderr, "evidence:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer, open func(string) (evidenceStore, io.Closer, error)) error {
	flags := flag.NewFlagSet("evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	list := flags.Bool("list", false, "list recorded executions")
	executionID := flags.String("execution", "", "render one execution as Markdown")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if (*list && *executionID != "") || (!*list && *executionID == "") {
		return errors.New("choose exactly one of -list or -execution <id>")
	}
	path := os.Getenv("MCP_DEPLOYMENT_CONTROLLER_DB_PATH")
	if path == "" {
		path = defaultDatabasePath
	}
	database, closer, err := open(path)
	if err != nil {
		return err
	}
	defer closer.Close()
	ctx := context.Background()
	if *list {
		executions, err := database.ListExecutions(ctx)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, renderList(executions))
		return err
	}
	value, err := database.GetExecution(ctx, *executionID)
	if err != nil {
		return err
	}
	plan, err := database.GetPlan(ctx, value.PlanID)
	if err != nil {
		return err
	}
	events, err := database.ListExecutionEvents(ctx, value.ID)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, renderExecution(value, plan, events))
	return err
}

func renderList(executions []execution.Execution) string {
	var output strings.Builder
	output.WriteString("# MCP Deployment Lab executions\n\n| Execution ID | Status | Started | Completed |\n| --- | --- | --- | --- |\n")
	for _, value := range executions {
		fmt.Fprintf(&output, "| `%s` | %s | %s | %s |\n", value.ID, value.Status, timestamp(value.CreatedAt), timestamp(value.CompletedAt))
	}
	return output.String()
}

func renderExecution(value execution.Execution, plan planning.Plan, events []execution.Event) string {
	var output strings.Builder
	output.WriteString("# MCP Deployment Lab execution evidence\n\n")
	fmt.Fprintf(&output, "- **Execution ID:** `%s`\n", value.ID)
	fmt.Fprintf(&output, "- **Plan ID:** `%s`\n", value.PlanID)
	fmt.Fprintf(&output, "- **Plan hash:** `%s`\n", value.PlanHash)
	if value.ProjectID != "" {
		fmt.Fprintf(&output, "- **Project ID fingerprint:** `%s`\n", fingerprint(value.ProjectID))
	}
	fmt.Fprintf(&output, "- **Final status:** `%s`\n", value.Status)
	fmt.Fprintf(&output, "- **Started:** %s\n", timestamp(value.CreatedAt))
	fmt.Fprintf(&output, "- **Completed:** %s\n", timestamp(value.CompletedAt))
	fmt.Fprintf(&output, "- **Duration:** %s\n", duration(value.CreatedAt, value.CompletedAt))
	if plan.Discovery.ProtocolVersion != "" {
		fmt.Fprintf(&output, "- **Downstream MCP protocol:** `%s`\n", plan.Discovery.ProtocolVersion)
	}
	if value.Error != "" {
		fmt.Fprintf(&output, "- **Final message:** %s\n", redact(value.Error))
	}
	output.WriteString("\n## Append-only timeline\n\n| Timestamp | Offset | Event type | Tool | Status before | Status after | Summary |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, event := range events {
		fmt.Fprintf(&output, "| %s | %s | %s | %s | %s | %s | %s |\n", timestamp(event.OccurredAt), offset(value.CreatedAt, event.OccurredAt), cell(event.EventType), cell(event.ToolName), cell(string(event.StatusBefore)), cell(string(event.StatusAfter)), cell(redact(event.Summary)))
	}
	return output.String()
}

func redact(value string) string {
	lower := strings.ToLower(value)
	for _, marker := range []string{"authorization", "bearer", "cookie", "token", "secret", "password", "api_key", "api-key", "pipeops_mcp_access_token", "mcp_deployment_controller_mcp_bearer_token"} {
		if strings.Contains(lower, marker) {
			return "[redacted: sensitive value]"
		}
	}
	return value
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.UTC().Format(time.RFC3339)
}

func duration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return "—"
	}
	return end.Sub(start).Round(time.Millisecond).String()
}

func offset(start, at time.Time) string {
	if start.IsZero() || at.IsZero() {
		return "—"
	}
	return at.Sub(start).Round(time.Millisecond).String()
}

func cell(value string) string {
	if value == "" {
		return "—"
	}
	return strings.ReplaceAll(value, "|", "\\|")
}
