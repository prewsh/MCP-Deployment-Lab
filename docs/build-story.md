# Building an MCP Deployment Controller: What Passed, What Failed, and What Remains

## What we built

MCP Deployment Controller is a Go service that is both an MCP server and a downstream PipeOps MCP client. It exposes a local dashboard and a stateless MCP `2026-07-28` endpoint, stores durable deployment workflows in SQLite, and keeps audit events append-only.

The implementation progressed from protocol discovery and fake workflows to read-only PipeOps planning, plan-hash approval, sandbox-gated execution, observation, and controlled failure experiments. The controller distinguishes `HEALTHY`, `FAILED`, and `UNKNOWN`; it does not equate an accepted deployment request with application health.

## How it was built

The server uses the official Go MCP SDK for Streamable HTTP, protocol discovery, schemas, and downstream MCP sessions. SQLite carries application state rather than MCP session state. PipeOps integration is deliberately provider-specific in v0.1: it records the negotiated tool catalog, validates a workspace/environment/server target, and allows only the approved `create_project` and `deploy_project` path after policy and approval checks.

The container package uses a multi-stage Go build and a minimal runtime image. Its database path is a mounted `/data` volume. The service remains loopback-only by default; a non-loopback bind needs the explicit `MCP_DEPLOYMENT_CONTROLLER_ALLOW_NETWORK_BIND=true` gate and authenticated private ingress.

## What passed

- Stateless MCP discovery and tool registration.
- Durable healthy and controlled failed fake workflows.
- PipeOps discovery and protocol compatibility fixtures.
- Plan-hash tampering rejection and fail-closed sandbox policy.
- Approval-required execution, exactly-once controller behavior, and health observation fixtures.
- Lost-response simulation resulting in `UNKNOWN` without blind retry.
- Failed wrong-port evidence resulting in a non-executing recovery proposal.
- Local HTTP dashboard and persisted timeline API end-to-end test.
- `go test ./...`, `go vet ./...`, and `go build ./cmd/server`.

## What failed or exposed limits

The first dashboard HTTP test exposed that the static UI expects the repository-root working directory; the test now starts from that intended runtime location. The desktop execution runner also ended a short-lived manual server process before an interactive UI fetch could complete, so no visual-browser claim is made.

Most importantly, no live PipeOps deployment has run from this controller. The connected Codex PipeOps session is not available as a bearer token to the Go process. The project source is now published at `prewsh/MCP-Deployment-Lab` and its Dockerfile was independently verified on GitHub, but PipeOps project creation has not been issued from this session.

## Next live experiment

Ensure PipeOps can read `prewsh/MCP-Deployment-Lab`, provide a controller-specific short-lived PipeOps write token through deployment secrets, and configure only the approved beta workspace/environment/server IDs. The first live test should use the normal path. The lost-response experiment should follow only after `ENABLE_FAULT_INJECTION=true` is deliberately enabled for that same sandbox.
