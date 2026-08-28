# Evidence files

Evidence files support reproducible debugging and experiments. Normally generate a controller execution record with:

```bash
go run ./cmd/evidence -list
go run ./cmd/evidence -execution exec_abc123
```

Or copy raw, reproducible provider output into a dated file. Do not rewrite evidence to make a result look better or worse. Redact credentials, cookies, authorization headers, and private infrastructure addresses before committing provider output.

The evidence CLI reads `MCP_DEPLOYMENT_CONTROLLER_DB_PATH`. If SQLite lives in a remote deployment volume, run the CLI where that database is accessible or use an exported copy. The project intentionally provides no unauthenticated remote evidence endpoint.
