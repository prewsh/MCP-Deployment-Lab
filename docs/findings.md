# Findings

This document separates reproducible live observations from questions that have not yet been tested. It does not treat an expected result as evidence.

| Experiment | Evidence source | Date | Evidence file |
| --- | --- | --- | --- |
| Live PipeOps healthy deployment | LIVE | 2026-08-28 | [live-pipeops-2026-08-28.md](evidence/live-pipeops-2026-08-28.md) |
| Live PipeOps wrong-port deployment | LIVE | 2026-08-28 | [live-pipeops-2026-08-28.md](evidence/live-pipeops-2026-08-28.md) |
| Live PipeOps run A1, before the uncertainty fix | LIVE | 2026-11-18 | [live-pipeops-2026-11-18.md](evidence/live-pipeops-2026-11-18.md) |
| Live PipeOps run A2, after the uncertainty fix | LIVE | 2026-11-18 | [live-pipeops-2026-11-18.md](evidence/live-pipeops-2026-11-18.md) |
| Live PipeOps run B, wrong port, after the fix | LIVE | 2026-11-18 | [live-pipeops-2026-11-18.md](evidence/live-pipeops-2026-11-18.md) |
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

## Live PipeOps runs A1 and A2: before and after the uncertainty fix

- **Evidence source:** LIVE
- **Date:** 2026-11-18
- **Raw evidence:** [operator capture with screenshots](evidence/live-pipeops-2026-11-18.md)

### Question

With the same plan inputs, what does the controller report before and after the change that treats an accepted `create_project` without a project ID as `UNKNOWN`?

### Setup

Run A1 used commit `546d231`, before the fix. Run A2 used commit `7d7c340`, which includes the fix (`8f8319b`). Both deployed `prewsh/CBT-App` from `master` on port `80` through MCP Inspector, as new PipeOps projects.

### Hypothesis / What I expected

A1 should report `FAILED` while the app runs. A2 should report `UNKNOWN` and make no further write.

### What was observed

A1: PipeOps reported "Deployment Went Live" at 9:20:14 PM (GMT+3); the controller reported `FAILED` at 9:20:15 PM; the URL served the app.

A2: the controller reported `UNKNOWN` at 9:52:34 PM while PipeOps showed the deployment as `DEPLOYING`. That first PipeOps deployment then failed (build passed, deploy failed, URL returned a PipeOps 404). A second deployment at 10:48:54 PM went live. What started that second deployment has not been confirmed; the controller made no further write.

### What MCP handled

Both hops negotiated MCP protocol version `2025-11-25`. The downstream client invoked `create_project` once per run.

### What the controller handled

It enforced the plan hash, approval and allowlist, then stopped after `create_project` because no project ID was found. It did not call `deploy_project` or observe the deployment in either run.

### Open questions

The `create_project` response contract is still the blocker. Until the controller receives a durable project ID, it cannot observe or reconcile an execution.

## Live PipeOps run B: wrong port after the uncertainty fix

- **Evidence source:** LIVE
- **Date:** 2026-11-18
- **Raw evidence:** [operator capture with screenshots](evidence/live-pipeops-2026-11-18.md)

### Question

When the configured port is wrong, does the platform's status match what users see?

### Setup

Same image, which listens on `80`, deployed with the project configured for port `3000`, using the fixed controller.

### Hypothesis / What I expected

PipeOps should report a readiness failure, as it did on 2026-08-28. The controller should report `UNKNOWN` because of the missing project ID.

### What was observed

PipeOps reported "Deployment Went Live" at 11:00:14 PM (GMT+3). The controller reported `UNKNOWN` at 11:00:16 PM. A browser request to the app URL at 20:01:14 UTC returned a Cloudflare 502 Bad Gateway, and still did later that night. Nginx logs show the container shutting down gracefully at 20:06:59 UTC.

### What MCP handled

The downstream client invoked `create_project` once.

### What the controller handled

It recorded `UNKNOWN` for the missing project ID. **It did not detect the 502;** the operator found it. No recovery proposal was created, because the controller never reached observation.

### Open questions

Provider status alone did not prove the app was reachable. Observation should include a check of the public URL, not only the provider's reported state.

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
