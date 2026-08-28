# Contributing

Thanks for improving MCP Deployment Lab.

## Local setup

Install the Go version declared in `go.mod`, then run:

```bash
go test ./...
go run ./cmd/server
```

Keep changes focused, add or update tests, and describe the behaviour you verified in a pull request.

## Experiments and safety

- Make experiments reproducible and retain unedited evidence where it is safe to do so.
- Never use production cloud credentials or production infrastructure in tests.
- Never commit credentials, headers, cookies, or exported environment files.
- Changes to approval, policy, authentication, execution, observation, and recovery boundaries require explicit review and evidence of their behaviour.
