# Roadmap

This is direction, not a delivery promise. MCP Deployment Lab will continue as an open-source foundation for safer agent-driven infrastructure changes.

- Capture the PipeOps `create_project` response contract so live executions get a durable project ID.
- Record a timeout or transport error during a write as `UNKNOWN`, not `FAILED`.
- Check the public URL during observation, not only the provider's reported state.
- Reconcile `UNKNOWN` executions using durable provider evidence without blind retries.
- Evaluate MCP Tasks integration for long-running operations.
- Add providers beyond PipeOps.
- Inspect repositories and workloads before planning.
- Improve deployment recommendations and resource sizing.
- Explore Terraform/OpenTofu planning boundaries.
- Strengthen identity with OAuth and richer policy controls.
- Improve observability and reusable Skill integrations.

Recovery will remain proposal-only unless a future design introduces a separately approved authority boundary.
