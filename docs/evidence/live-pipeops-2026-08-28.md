# Live PipeOps operator capture — 2026-08-28

- **Source:** PipeOps console views and controller/MCP Inspector results captured by the operator.
- **Sanitization:** Credentials were never captured. The provider's private readiness-probe IP address is intentionally omitted.

## Healthy CBT-App run

- Project: `cbt-app-baseline`
- Repository: `prewsh/CBT-App`, branch `master`
- Configured port: `80`
- PipeOps dashboard state: running
- Public result: the Quick Quiz page rendered successfully.
- Controller result before Pass B: `FAILED`, with `create_project failed: PipeOps create_project response did not include a project ID`.

## Wrong-port CBT-App run

- Project: `cbt-app-wrong-port`
- Repository: `prewsh/CBT-App`, branch `master`
- Configured port: `3000`
- Runtime log: Nginx completed startup and started worker processes.
- Deployment pipeline: commit, scan, and build succeeded; deploy failed.
- PipeOps event: `Readiness probe failed: dial tcp [private-address]:3000: connect: connection refused`.

This is a sanitized transcription of the provider output, not a replacement for the original operator capture.
