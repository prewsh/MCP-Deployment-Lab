# Architecture

## Purpose

MCP Deployment Lab separates an agent's request to deploy from the authority to make an infrastructure change. It is an upstream MCP server for clients and a downstream MCP client for PipeOps. Its HTTP MCP transport is stateless; plans, approvals, executions, and audit events are application state stored in SQLite.

```text
AI / MCP client
    │
    │ Streamable HTTP
    ▼
HTTP access boundary
    ├─ loopback
    ├─ public-readonly
    └─ authenticated bearer
    │
    ▼
MCP Deployment Lab
    ├─ echo / inspect_request
    ├─ fake workflow simulation
    ├─ PipeOps discovery and planning
    ├─ plan-bound approval and policy validation
    ├─ approved execution and observation
    ├─ proposal-only recovery helper
    └─ SQLite audit trail
    │
    ▼
PipeOps MCP
    ├─ list_servers / list_environments
    ├─ create_project
    ├─ deploy_project
    └─ observation tools
```

## HTTP access boundary

The access boundary is evaluated before the SQLite store and MCP handler are started.

- **Loopback:** the default `127.0.0.1:8080` configuration has no inbound authentication and is intended for local development.
- **Public-readonly:** `MCP_DEPLOYMENT_CONTROLLER_PUBLIC_MODE=true` exposes only a static `GET /` page and `GET /healthz`. MCP and `/api/executions/*` return empty `404` responses.
- **Authenticated:** `MCP_DEPLOYMENT_CONTROLLER_MCP_BEARER_TOKEN` protects MCP requests and execution timelines with an exact `Authorization: Bearer <token>` value checked using `crypto/subtle.ConstantTimeCompare`.

A non-loopback listener needs `MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true` and must choose either public-readonly or authenticated mode. Public-readonly and bearer authentication are mutually exclusive.

The static public page never reads execution data. The local timeline page renders execution fields with DOM text nodes rather than HTML interpolation.

## Planning, approval, and policy

`plan_pipeops_deployment` uses PipeOps discovery plus `list_servers` and `list_environments` to create a durable proposal. It does not perform a write.

The proposal contains a SHA-256 hash over its serialized execution intent. `decide_pipeops_plan` records one immutable approval or denial that names the exact hash. Before writing, `execute_pipeops_plan` rechecks:

1. the stored plan hash;
2. the matching approved decision;
3. `PIPEOPS_WRITE_ENABLED=true`;
4. the configured workspace, environment, and server allowlist; and
5. the constrained GitHub/Dockerfile execution contract.

Only then can the controller invoke `create_project` followed by `deploy_project`. Delete, stop, arbitrary tool, and recovery writes are not implemented.

## Execution and observation

An execution record is unique per plan, preventing repeat execution requests from issuing another controller-managed write. The execution timeline records `EXECUTING`, `OBSERVING`, `HEALTHY`, `FAILED`, or `UNKNOWN`.

After PipeOps accepts a deployment, the controller observes build logs, project state, and runtime logs. The PipeOps adapter reduces provider data to sanitized fields such as state, failure kind, and detected port; it does not persist raw logs or provider addresses. A readiness-probe connection refusal is represented as `READINESS_PROBE_CONNECTION_REFUSED` plus the detected port.

If PipeOps accepts `create_project` but does not return a durable project identifier in a supported response location, the controller records `UNKNOWN` and does not retry the write. A missing response in the intentional lost-response experiment, an observation error, cancellation, or timeout also becomes `UNKNOWN`.

The recovery helper is proposal-only. It can formulate a port-change proposal only when a failed observation has the structured readiness-probe failure kind and a different detected port. It never parses generic prose, changes configuration, restarts, redeploys, or deletes infrastructure.

## Persistence and audit

SQLite stores fake deployments, their append-only events, PipeOps discovery records, immutable plan JSON, immutable approvals, executions, and append-only execution events. SQLite triggers reject updates and deletes to the audit tables.

The local `GET /` timeline page can load an execution through `GET /api/executions/{id}` in loopback or authenticated mode. The API is intentionally unavailable in public-readonly mode.

## Current limits

The project currently supports PipeOps only, has no MCP Tasks integration or automated `UNKNOWN` reconciliation, and uses a shared inbound bearer token rather than OAuth for authenticated remote access. It is intended for sandbox infrastructure while these controls and provider evidence contracts mature.
