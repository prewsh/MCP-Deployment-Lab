# Findings

## M5-1 — Accepted is not known success

The lost-response fixture calls the downstream deployment operation, withholds the response at the controller boundary, and records `UNKNOWN`. The controller does not retry. This demonstrates that a missing result after a non-idempotent action is uncertainty, not failure.

## M5-2 — Recovery is another proposal

When failed observation evidence identifies a different application port, the controller records `RECOVERY_PROPOSED` with the detected and proposed ports plus `update configuration → redeploy → verify`. It does not apply that change. Recovery requires a new plan and approval boundary.

## Limits

These findings come from controlled MCP fixtures, not a live PipeOps deployment. A live experiment requires the controller-specific write token and the explicit `dark-prometheus-beta` allowlist settings.
