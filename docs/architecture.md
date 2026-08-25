# Milestone 4 architecture

## Purpose

Milestone 4 turns a PipeOps plan into a controlled authority boundary. A plan carries an immutable hash; a decision binds that hash to an approver; a local policy admits only the configured sandbox; execution rechecks all three before any downstream write.

```text
MCP client
    │
    │ server/discover, tools/list, tools/call
    ▼
Streamable HTTP handler (Stateless: true)
    ▼
MCP server
    ├─ echo
    ├─ inspect_request
    ├─ start_fake_deployment
    └─ get_deployment
            │
            ▼
   deployment.Service (background simulation)
            │
            ▼
 SQLite: deployments + append-only deployment_events
            │
            ├─ pipeops_discoveries (protocol + tool catalog)
            └─ deployment_plans (immutable proposal JSON)

MCP server tools
    │
    │ outbound MCP with short-lived bearer token
    ▼
PipeOps remote MCP endpoint
    ├─ tools/list
    ├─ list_servers             (read)
    └─ list_environments        (read)

Future, not called in this milestone:
    ├─ delete / stop / recovery writes
    └─ arbitrary provider tools
```

## Protocol lifecycle

MCP versions through `2025-11-25` use `initialize` followed by `notifications/initialized`. MCP `2026-07-28` removes that handshake: requests carry protocol version and client capabilities in `_meta`, and servers implement `server/discover` so clients can learn capabilities and choose a compatible version.

`Stateless: true` means this HTTP handler does not maintain MCP HTTP sessions or process server-to-client requests. It does **not** prevent a future application from storing deployment records, approvals, or audit events in its own database.

## Downstream protocol and credentials

The controller uses the official Go MCP SDK's Streamable HTTP client. The client records the actual `Mcp-Protocol-Version` header used after negotiation, along with the downstream tool catalog. It supports a remote service that negotiates an older compatible version, as verified by the automated test.

`PIPEOPS_MCP_ACCESS_TOKEN` is supplied only to the controller process. The HTTP transport injects it into the `Authorization` header for outbound requests, but no token value is included in structured logs, tool responses, SQLite, or plan evidence. The desktop application's PipeOps connector session deliberately is not assumed to be available to the local process.

## Authority boundary

`plan_pipeops_deployment` validates a supplied workspace/environment pair through `list_servers` and `list_environments`. It confirms the linked server exists and is `available`, then writes a local `PROPOSED` plan. The plan enumerates anticipated write operations but does not call them. There is no provider abstraction in this milestone: the contract and validation are deliberately PipeOps-specific.

The initial approved sandbox target is `dark-prometheus-beta` on `dark-prometheus` in `eu-west-2`. Its IDs are intentionally runtime configuration, not hard-coded application configuration. Production is never an M4 target.

```text
PROPOSED plan + SHA-256 hash
          │
          ▼
immutable APPROVED / DENIED decision
          │
          ▼
recheck hash + approval + write switch + three-ID allowlist
          │
          ▼
create_project → deploy_project → observe
```

The policy requires `PIPEOPS_WRITE_ENABLED=true` and matching `PIPEOPS_ALLOWED_WORKSPACE_ID`, `PIPEOPS_ALLOWED_ENVIRONMENT_ID`, and `PIPEOPS_ALLOWED_SERVER_ID`. It admits only the constrained GitHub/Dockerfile project contract. Downstream MCP annotations are retained in the discovered catalog as safety evidence, but the controller's explicit local policy makes the authorization decision.

## Execution and observation

The execution record is unique per plan, preventing the controller from issuing duplicate writes for a repeated execution request. It records `EXECUTING`, `OBSERVING`, `HEALTHY`, `FAILED`, or `UNKNOWN`, plus an append-only timeline. The controller calls `get_project_build_logs`, `get_project`, and `get_project_logs` until it observes terminal evidence. A timeout, cancellation, or failed observation call becomes `UNKNOWN`; it does not justify a blind retry.

## Milestone 5 failure boundary

The only injected fault is `LOST_RESPONSE_AFTER_DEPLOY`. It is guarded by `ENABLE_FAULT_INJECTION=true` as well as the exact M4 sandbox policy. It occurs only after `deploy_project` has been called successfully and forces `UNKNOWN`; observation and automatic retry are intentionally skipped.

For a failed observation that identifies a port mismatch, the controller appends a `RECOVERY_PROPOSED` event. It is explanatory evidence, not an authorization or write path. The local `GET /` view accepts `?execution_id=...` and reads that execution's append-only events from `GET /api/executions/{id}`; it includes accessible status color, a skip link, responsive layout, and reduced-motion support.

## Boundaries

This milestone deliberately contains no delete/stop/recovery write path, fault injection, MCP Tasks implementation, provider abstraction, or frontend. `start_fake_deployment` still has only a local side effect: it writes a SQLite workflow record and simulates states in a background goroutine.

## Workflow state and audit events

The healthy fake lifecycle is `QUEUED → DEPLOYING → BUILDING → VERIFYING → HEALTHY`. Passing `fail_at: "BUILDING"` takes the controlled failure path `QUEUED → DEPLOYING → BUILDING → FAILED`.

Each transition is written in the same SQLite transaction as the updated deployment status. `deployment_events` contains identifiers, timestamp, event source/type, status before/after, result summary, protocol version where known, and a hash rather than raw tool arguments. SQLite triggers reject updates and deletes against this table.

MCP server/session state remains stateless. The SQLite records are application workflow state and will later be the controller's durable timeline; they are not MCP Tasks. PipeOps discoveries and plans are also durable evidence, but not permission to execute the proposed write operations.

## Safe request inspection

`inspect_request` returns only the SDK-exposed protocol version, client identity, and client capabilities. HTTP headers, authentication data, cookies, request bodies, and tool arguments are excluded from both tool output and request logs.
