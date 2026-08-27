# MCP Deployment Lab

MCP Deployment Lab is an open-source control plane and experimentation project for safer, observable agent-driven infrastructure changes through Model Context Protocol (MCP).

> What does it take to safely orchestrate a long-running, consequential cloud operation through a stateless agent/tool protocol?

## Why it exists

Agent intent is not authority to change infrastructure. A successful tool invocation is also not evidence of a successful real-world deployment. This project explores the boundaries between planning, explicit approval, constrained authority, observation, uncertainty, and auditability.

The current implementation is experimental and intended for sandbox infrastructure.

## Design principles

1. Approval should bind to immutable execution intent, not merely a mutable plan identifier.
2. Infrastructure authority should fail closed when required policy context is missing.
3. Consequential operations may end in materially different states: `HEALTHY`, `FAILED`, or `UNKNOWN`.

## Architecture

```text
AI / MCP client
       │
       ▼
MCP Deployment Lab
       ├─ discovery
       ├─ planning
       ├─ approval
       ├─ policy
       ├─ execution
       ├─ observation
       ├─ recovery proposal
       └─ audit
       │
       ▼
PipeOps MCP
       │
       ▼
sandbox infrastructure
```

The controller is an MCP server upstream and an MCP client downstream. It stores application workflow state, plans, approvals, and timelines in SQLite; its Streamable HTTP MCP transport is stateless.

## Current capabilities

- Local, durable fake deployment workflows with append-only audit events.
- Downstream PipeOps capability discovery and read-only target validation.
- SHA-256 plan hashes, one immutable approval decision per plan, and a fail-closed sandbox allowlist.
- Approved PipeOps execution through only `create_project` and `deploy_project`.
- Build, project, and runtime-log observation that classifies a deployment as `HEALTHY`, `FAILED`, or `UNKNOWN`.
- A proposal-only recovery helper for failed observations that contain a detected port.
- Loopback, public-readonly, and authenticated HTTP access modes.

## MCP tools

| Tool | Purpose |
| --- | --- |
| `echo` | Verifies a tool call without changing state. |
| `inspect_request` | Returns safe protocol and client metadata. |
| `start_fake_deployment` | Starts a local simulated workflow; it creates no cloud resources. |
| `get_deployment` | Reads a fake workflow and its append-only events. |
| `discover_pipeops` | Records downstream PipeOps capabilities. |
| `plan_pipeops_deployment` | Validates a PipeOps target with read-only operations and stores a proposal. |
| `decide_pipeops_plan` | Records an immutable approval or denial for an exact plan hash. |
| `execute_pipeops_plan` | Rechecks approval and policy, then invokes only `create_project` and `deploy_project`. |

## Quickstart

Prerequisite: Go `1.27.0` as declared in `go.mod`.

```bash
go test ./...
go run ./cmd/server
```

The default loopback server listens on `http://127.0.0.1:8080`. No inbound authentication is required in this local-only mode. The default database is `data/mcp-deployment-controller.db`; set `MCP_DEPLOYMENT_CONTROLLER_DB_PATH` to use another SQLite file.

Connect an MCP-capable client to that URL, call `start_fake_deployment`, then call `get_deployment` with its returned `deployment_id`. The local timeline view is available at `/?execution_id=<execution-id>`.

## Access modes

| Mode | Configuration | Exposed surface |
| --- | --- | --- |
| `loopback` | Default `127.0.0.1` listener; no public mode or bearer token. | MCP and execution timeline API are available locally without inbound authentication. |
| `public-readonly` | `MCP_DEPLOYMENT_CONTROLLER_PUBLIC_MODE=true`; a non-loopback listener also needs `MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true`. | Only `GET /` (a static status page) and `GET /healthz`. MCP and `/api/executions/*` return empty `404` responses. |
| `authenticated` | Set `MCP_DEPLOYMENT_CONTROLLER_MCP_BEARER_TOKEN`; a non-loopback listener also needs `MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true`. | MCP and execution timelines require `Authorization: Bearer <token>`. `GET /` and `GET /healthz` remain public. |

`MCP_DEPLOYMENT_CONTROLLER_PUBLIC_MODE=true` and `MCP_DEPLOYMENT_CONTROLLER_MCP_BEARER_TOKEN` are mutually exclusive. A non-loopback listener without either public-readonly mode or a bearer token is rejected at startup.

## PipeOps configuration

Read-only planning requires a controller-specific, short-lived PipeOps token:

```bash
export PIPEOPS_MCP_ACCESS_TOKEN='your-short-lived-pipeops-token'
# Optional; this is the default:
export PIPEOPS_MCP_ENDPOINT='https://mcp.pipeops.app/mcp'
go run ./cmd/server
```

The controller does not persist or log this outbound credential. Without it, local fake workflows still work while PipeOps tools return a configuration error.

PipeOps writes are disabled by default. A controlled sandbox execution additionally needs all four settings below:

```bash
export PIPEOPS_WRITE_ENABLED=true
export PIPEOPS_ALLOWED_WORKSPACE_ID='sandbox-workspace-id'
export PIPEOPS_ALLOWED_ENVIRONMENT_ID='sandbox-environment-id'
export PIPEOPS_ALLOWED_SERVER_ID='sandbox-server-id'
```

The allowlist is checked both when a plan is approved and immediately before execution. Do not use production credentials or production targets with this experimental project.

## Safety model and limitations

- Approval binds an exact immutable plan hash.
- Writes require explicit approval, `PIPEOPS_WRITE_ENABLED=true`, and an exact workspace/environment/server allowlist.
- Recovery is proposal-only: it never changes configuration, restarts, redeploys, or deletes resources.
- `UNKNOWN` is not failure or success. The controller does not blindly retry after an uncertain mutation.
- PipeOps is the only provider currently implemented.
- Health classification is experimental and not production-grade.
- MCP Tasks is not implemented; deployment handles are application-level state.
- `UNKNOWN` has no automated reconciliation workflow.
- Remote authentication is shared bearer authentication, not OAuth.
- Infrastructure sizing and recommendations are not implemented.
- The current PipeOps observation adapter emits a generic failed summary, so live wrong-port evidence is not yet available to the recovery helper. That evidence path will be updated after a real provider payload is captured.

See [the architecture document](docs/architecture.md) for the current design and [the build story](docs/build-story.md) and [learning log](docs/learning-log.md) for project history.

## License

Licensed under [Apache-2.0](LICENSE).
