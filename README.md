# MCP Deployment Controller

An open-source learning and orchestration project for consequential deployment workflows through Model Context Protocol (MCP).

Milestone 4 is a stateless MCP `2026-07-28` controller with durable plans, plan-bound approval, a fail-closed PipeOps sandbox policy, execution, and health observation.

## Architecture

```text
MCP client
    │
    │ Streamable HTTP / MCP 2026-07-28
    ▼
MCP Deployment Controller
    ├─ echo
    ├─ inspect_request
    ├─ start_fake_deployment
    └─ get_deployment
          │
          ▼
       SQLite workflow state + append-only audit events
    │
    └─ PipeOps MCP client
          ├─ MCP capability discovery
          ├─ list_servers
          ├─ list_environments
          ├─ create_project + deploy_project (approved sandbox only)
          └─ build/project/runtime observation
```

The controller records the protocol version actually negotiated with PipeOps rather than assuming one. It stores the discovered tool catalog and each immutable deployment proposal in SQLite. The first approved testing target is the `dark-prometheus-beta` environment on `dark-prometheus` in `eu-west-2`; callers still supply their own workspace and environment IDs, so this open-source project contains no account-specific configuration.

## Why these tools

- **Go** keeps the controller close to the PipeOps MCP ecosystem and offers a small, efficient single-binary server.
- **Official Go MCP SDK v1.7.0** implements the MCP wire protocol, discovery, negotiation, validation, and Streamable HTTP transport instead of this project reimplementing them.
- **`log/slog`** is Go’s built-in structured logger; it produces useful local JSON logs without adding a logging platform in the learning milestone.
- **SQLite via `modernc.org/sqlite`** is pure Go, so contributors do not need a C compiler. It provides durable local workflow state and transactions without requiring a managed database.

## Run locally

The server binds only to `127.0.0.1:8080` and does not require inbound authentication while it remains local-only.

```bash
go run ./cmd/server
```

The default database is `data/mcp-deployment-controller.db`. Set `MCP_DEPLOYMENT_CONTROLLER_DB_PATH` to use another local SQLite file.

## Container deployment

This is a Go backend with an embedded local dashboard, not a static frontend. Build it as a container with `docker build -t mcp-deployment-controller .`. The image stores SQLite data at `/data`, so production needs a persistent volume.

The process binds to loopback by default. A container platform must set `MCP_DEPLOYMENT_CONTROLLER_LISTEN_ADDR=0.0.0.0:8080` **and** `MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true`; do this only behind authenticated private ingress. This application does not yet implement upstream MCP authentication, so Vercel and Netlify are not appropriate production hosts.

### Enable PipeOps planning and sandbox execution

The controller is a separate MCP client, so it needs its own short-lived PipeOps bearer token. A Codex desktop connector session is not automatically available to a local Go process. Export a token restricted to PipeOps read access in the shell that starts the controller; do not put it in a source file, database, or MCP tool call.

```bash
export PIPEOPS_MCP_ACCESS_TOKEN='your-short-lived-pipeops-token'
# Optional; this is the default:
export PIPEOPS_MCP_ENDPOINT='https://mcp.pipeops.app/mcp'
go run ./cmd/server
```

With the token absent, fake local workflows still work. `discover_pipeops` and `plan_pipeops_deployment` instead return a clear configuration error.

Writes are disabled by default. To enable execution, the process must also be started with all four settings below. They deliberately have no defaults; use the IDs of `dark-prometheus-beta` and its server only.

```bash
export PIPEOPS_WRITE_ENABLED=true
export PIPEOPS_ALLOWED_WORKSPACE_ID='approved-workspace-id'
export PIPEOPS_ALLOWED_ENVIRONMENT_ID='dark-prometheus-beta-environment-id'
export PIPEOPS_ALLOWED_SERVER_ID='dark-prometheus-server-id'
```

Do not configure production IDs. A plan outside this exact three-ID allowlist is rejected both when it is approved and immediately before execution.

Run the automated tests:

```bash
go test ./...
```

## Inspect the protocol

Use an MCP-capable client to connect to `http://127.0.0.1:8080` and call `tools/list` or any tool. The Go SDK client sends `server/discover` automatically before normal requests when negotiating MCP `2026-07-28`.

For a direct discovery request:

```bash
curl -sS http://127.0.0.1:8080 \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  --data '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"curl","version":"v0"}}}}'
```

The response includes supported protocol versions, server identity, capabilities, and cache hints. Stateless mode means the response does not establish an MCP HTTP session; it does **not** make future deployment workflow state stateless.

## Milestone 2 workflow

`start_fake_deployment` now stores a workflow and returns a durable `deployment_id`. The simulated healthy path is:

```text
QUEUED → DEPLOYING → BUILDING → VERIFYING → HEALTHY
```

Pass `fail_at: "BUILDING"` to deliberately produce:

```text
QUEUED → DEPLOYING → BUILDING → FAILED
```

Call `get_deployment` with the returned ID to retrieve the current workflow status and its append-only audit events. The tool is an application-level handle, not an implementation of the optional MCP Tasks extension.

## Milestone 3 PipeOps plan

`discover_pipeops` opens a downstream MCP session, records the negotiated protocol and tool catalog, then persists that evidence. `plan_pipeops_deployment` requires an application name, repository, branch, port, PipeOps workspace ID, and environment ID. It calls exactly these downstream read operations:

1. `list_servers` to find the server associated with the environment and verify that it is available.
2. `list_environments` to verify the requested environment belongs to the supplied workspace.

It returns and persists a `PROPOSED` plan. The plan names `create_project` and, when available, `deploy_project` only as future write operations requiring approval. They are never invoked in Milestone 3.

## Milestone 4 authority and execution

Every generated plan has a SHA-256 `hash` over its execution intent. Call `decide_pipeops_plan` with the plan ID, its exact hash, `APPROVED` or `DENIED`, and the local approver identity. A decision is immutable. `APPROVED` is accepted only if the local sandbox policy permits the plan.

`execute_pipeops_plan` needs the same plan ID and hash. It rechecks the persisted hash, approval, and sandbox policy before it calls PipeOps. It then creates the project, submits a deployment, and polls build logs, project state, and runtime logs. The controller returns `HEALTHY`, `FAILED`, or `UNKNOWN`; a successful `deploy_project` response alone is never treated as healthy. Repeating execution for a plan returns the existing execution record rather than issuing another write.

## Milestone 5 failure laboratory

`execute_pipeops_plan` accepts one optional experiment: `fault: "LOST_RESPONSE_AFTER_DEPLOY"`. It is rejected unless both the existing sandbox allowlist and `ENABLE_FAULT_INJECTION=true` are present. The controller calls PipeOps, intentionally withholds the accepted result, records `UNKNOWN`, and never retries automatically.

A failed observation mentioning a different port produces a `RECOVERY_PROPOSED` event: update configuration, redeploy, then verify. It never repairs the project automatically. The localhost `GET /` view is an industrial timeline reference for these append-only events; it is not a public dashboard.

## Safety boundary

- Listens only on loopback (`127.0.0.1`).
- Uses a separately supplied, short-lived PipeOps bearer token only for outbound MCP calls; it is never persisted or logged.
- Does not log request bodies, tool arguments, cookies, authorization headers, or tokens.
- Writes require an exact approved plan hash, `PIPEOPS_WRITE_ENABLED=true`, and an exact workspace/environment/server allowlist.
- Executes only `create_project` and `deploy_project`; delete, stop, recovery, and arbitrary PipeOps tools are not implemented.
- Treats a lost or timed-out observation as `UNKNOWN`, not as a failed or successful deployment.
- The only simulated fault is `fail_at: "BUILDING"`; no provider or arbitrary-target fault injection exists.

## License

Licensed under [Apache-2.0](LICENSE).
