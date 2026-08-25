# Learning log

## Milestone 1 — Protocol

### Question

How does a stateless MCP `2026-07-28` server discover client capabilities and serve tools without the legacy initialization lifecycle?

### What I expected

The official Go SDK would own protocol-level discovery and per-request metadata handling, while the application would own only tool behavior and its own future deployment workflow state.

### What I built

A local-only Streamable HTTP server with `Stateless: true` and three immediate tools: `echo`, `inspect_request`, and `start_fake_deployment`.

### Evidence

`go test ./...` passed. A live localhost check sent a `server/discover` request using protocol `2026-07-28`; the server returned that version in `supportedVersions` and did not return an `Mcp-Session-Id` header. A subsequent `tools/list` response exposed `echo`, `inspect_request`, and `start_fake_deployment` with generated JSON schemas. Live calls returned the echoed text, the supplied client identity and protocol metadata, and an immediate fake deployment result respectively.

### What MCP handles

Protocol negotiation, `server/discover`, per-request protocol metadata, tool schemas, input decoding, and Streamable HTTP framing are handled by the Go SDK.

### What the application handles

Tool behavior, safe logging, future deployment workflow state, authorization, approvals, and provider interactions remain application concerns.

### Questions remaining

- How does a real downstream PipeOps MCP server negotiate with this controller?
- Which application-state model best maps to the Tasks extension when client and SDK support are available?

## Milestone 2 — Workflow

### Question

Can a stateless MCP server own a durable, observable deployment workflow without treating an MCP request or session as the workflow itself?

### What I expected

The application would need a separate store for deployment state and a separate append-only event timeline. SQLite transactions should keep a status transition and its audit evidence consistent.

### What I built

SQLite-backed `deployments` and `deployment_events` tables, a local fake workflow service, durable deployment handles, `get_deployment`, and one controlled simulated failure point: `BUILDING`.

### What happened

The full automated suite passed, including healthy and failed workflow paths, a reopen/persistence check, and a SQLite trigger check that rejected an attempt to update an audit event. A live MCP request with `fail_at: "BUILDING"` returned a queued deployment handle. Later `get_deployment` returned `FAILED` and four events: initial request, `DEPLOYING`, `BUILDING`, and `SIMULATED_FAILURE`.

### What I now understand

Stateless MCP HTTP requests can trigger and inspect durable application workflows. The protocol does not persist deployment state for the controller: the application owns status transitions, retry decisions, audit history, and the relationship between a tool call and a real-world operation.

### Questions remaining

- How should the controller reconcile a downstream tool result that is lost after the provider accepts a real operation?
- Which observations should be required before a real deployment can be called healthy?

## Milestone 3 — Downstream PipeOps planning

### Question

Can the controller act as a downstream MCP client safely enough to discover PipeOps, validate a selected sandbox target, and create a durable deployment proposal without taking a real infrastructure action?

### What I built

A PipeOps-specific Streamable HTTP MCP client with a separate bearer-token configuration boundary, protocol-version observation, tool discovery, target inspection, and durable SQLite records for discovery evidence and proposed plans. The controller exposes `discover_pipeops` and `plan_pipeops_deployment` to its upstream MCP clients.

### What happened

The controller's PipeOps client was tested against authenticated local MCP fixtures. It successfully discovered the tool catalog using current and legacy-compatible protocol versions, authenticated outbound requests with the configured bearer header, validated the `dark-prometheus-beta` fixture target, and persisted a `PROPOSED` plan. The test registered simulated `create_project` and `deploy_project` tools and verified neither was called.

The Codex PipeOps connector was independently confirmed authenticated and showed the approved testing server `dark-prometheus` in `eu-west-2`, with beta and production environments. The controller itself has not yet made a live PipeOps request because the desktop connector's credential is not exposed to the local Go process. A controller-specific short-lived read token is required for that live check.

### What I now understand

Downstream MCP discovery and an application deployment workflow are distinct. Protocol negotiation is an SDK concern; the application must store its own evidence, decide exactly which tools may be called, validate the target, and place a durable approval boundary before any write. A plan is an auditable proposal, not an execution permission.

### Questions remaining

- What exact PipeOps create/deploy result and observation tools should Milestone 4 require before considering an execution successful?
- How should an approval bind a plan, target, tool schema, and argument hash so that it cannot be replayed or redirected?

## Milestone 4 — Authority and observation

### Question

What must happen between an agent proposing a deployment and a controller performing a consequential PipeOps write?

### What I built

SHA-256 plan hashes, immutable approval decisions, a fail-closed local sandbox policy, one execution record per plan, and an append-only execution timeline. The only executable actions are `create_project` and `deploy_project`; the controller then collects build, project, and runtime-log evidence before classifying the result.

### What happened

Automated tests verified that a tampered plan hash is rejected, a write cannot occur without a matching approval, a permitted plan calls the fake PipeOps client once and reaches `HEALTHY`, and a repeated execution returns the existing record without a second write. The full Go test suite and `go vet ./...` passed.

No live PipeOps write was made during this implementation. The local controller still needs its own write-capable short-lived PipeOps token and explicit sandbox allowlist environment variables; the Codex connector credential is not exposed to it.

### What I now understand

Approval must bind to immutable intent, and it is not sufficient on its own: target policy must be checked again at execution time. An accepted deploy operation is only an intermediate result. The controller's terminal status must be based on later observation, and ambiguity must be represented as `UNKNOWN` rather than hidden by retrying a potentially non-idempotent action.

## Milestone 5 — Failure and recovery proposals

### Question

How should the controller distinguish a known deployment failure from uncertainty after a consequential downstream action?

### What I built

A single sandbox-gated lost-response experiment, wrong-port recovery proposal detection, a durable `RECOVERY_PROPOSED` timeline event, a localhost timeline view, and factual findings documentation.

### What happened

Automated tests proved that the lost-response injection produces `UNKNOWN`, does not observe or retry, and is refused unless fault injection is explicitly enabled. A failed observation naming port 3000 for a plan configured at port 8080 produced a recovery-proposal event without a write. The complete test suite and `go vet ./...` passed.

The local UI asset and its read-only execution endpoint were structurally verified. The desktop runner ended the short-lived manual `go run` process before its HTTP fetch could complete, so no visual-browser claim is recorded.

### What I now understand

After an accepted non-idempotent request, the absence of a response is a distinct operational state. Treating it as failure encourages unsafe retries; treating it as success hides risk. The correct v0.1 behavior is `UNKNOWN`, a preserved timeline, and a human decision about reconciliation. A recovery suggestion is also only intent until it crosses a separate approval boundary.
