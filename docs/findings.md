# Findings

This document separates reproducible live observations from questions that have not yet been tested. It does not treat an expected result as evidence.

| Experiment | Evidence source | Date | Evidence file |
| --- | --- | --- | --- |
| Live PipeOps healthy deployment | LIVE | 2026-08-28 | [live-pipeops-2026-08-28.md](evidence/live-pipeops-2026-08-28.md) |
| Live PipeOps wrong-port deployment | LIVE | 2026-08-28 | [live-pipeops-2026-08-28.md](evidence/live-pipeops-2026-08-28.md) |
| Lost-response-after-deploy experiment | NOT YET RUN | — | — |

## Live PipeOps healthy deployment

- **Evidence source:** LIVE
- **Date:** 2026-08-28
- **Raw evidence:** [sanitized operator capture](evidence/live-pipeops-2026-08-28.md)

### Question

Can the CBT-App GitHub repository, with its added Nginx Dockerfile, run on the approved PipeOps sandbox target?

### Setup

The operator planned, explicitly approved, and executed a deployment of `prewsh/CBT-App` from `master` with port `80` through the local controller and MCP Inspector.

### Hypothesis / What I expected

The application should become reachable through PipeOps. The controller should retain a durable handle and independently observe the terminal state.

### What was observed

PipeOps reported the project running and served the Quick Quiz page. The controller received a successful-looking `create_project` tool result but could not extract a project ID, so its local execution record was incorrectly marked `FAILED` in the pre-Pass-B build.

### What MCP handled

The upstream controller accepted the plan, approval, and execution request. Its downstream PipeOps client invoked `create_project`.

### What the controller handled

It enforced the exact plan hash and sandbox policy. It did not reach its explicit `deploy_project` or observation steps because the provider response lacked a recognised project-ID location.

### Open questions

What stable PipeOps `create_project` response contract should the adapter support? Until it has a durable project ID, the controller must treat the accepted write as `UNKNOWN` and must not retry it.

## Live PipeOps wrong-port deployment

- **Evidence source:** LIVE
- **Date:** 2026-08-28
- **Raw evidence:** [sanitized operator capture](evidence/live-pipeops-2026-08-28.md)

### Question

How does PipeOps report a workload configured with an incorrect readiness/service port?

### Setup

The same Nginx-based CBT-App image, which listens on port `80`, was deployed with the PipeOps project configured for port `3000`.

### Hypothesis / What I expected

The image should start, while the provider readiness check should fail because port `3000` has no listener.

### What was observed

Commit, scan, and build succeeded. PipeOps created and started the container, then recorded a readiness-probe connection refusal for port `3000`; the deployment pipeline reached `FAILED`. The application logs show normal Nginx startup, not an application crash.

### What MCP handled

The controller again initiated the approved project creation but did not obtain a project ID from the provider result.

### What the controller handled

The pre-Pass-B controller recorded `FAILED` at project creation and did not observe this provider failure or create a recovery proposal. Pass B changes that local classification to `UNKNOWN` for an accepted write without a handle, and represents future readiness-probe evidence as structured, sanitized fields.

### Open questions

Capture and support the provider's durable project-ID response contract, then repeat the experiment so the controller can independently observe the same evidence and create a proposal-only recovery recommendation.

## Lost-response-after-deploy experiment

- **Evidence source:** NOT YET RUN
- **Date:** —
- **Raw evidence:** —

### Question

What durable evidence remains when PipeOps accepts a deployment but the controller intentionally withholds the downstream response?

### Setup


### Hypothesis / What I expected


### What was observed


### What MCP handled


### What the controller handled


### Open questions

Which provider-side records are sufficient to reconcile an `UNKNOWN` execution without retrying a non-idempotent write?
