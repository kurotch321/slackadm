# Contributing

Go 1.26.9 or newer is required. Run `gofmt -w cmd internal`, `go vet ./...`,
`go test ./...`, and `go build ./cmd/slackadm` before opening a pull request.
On a CGO-capable host, also run `go test -race ./...`.

Tests must run without Slack credentials. Use fictional IDs and `example.invalid`
emails. `slacktest` is used for API integration, and `httptest` for fault injection.
Do not commit tokens, local configuration, real user data or generated batch plans.

Operation definitions in `internal/service/registry.go` drive help, schema and batch
validation. Changes to output or exit codes require contract tests. Preserve the
no-retry guarantee for ambiguous writes and environment-token precedence.

Use a feature branch and pull request. Describe the problem, resulting behavior,
and validation. Other-person approval is not required; CI must pass. Releases are
created as drafts by `v*` tags and must be published separately by a maintainer.
