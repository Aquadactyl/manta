# Contributing to Manta

Manta is the Aquadactyl server daemon maintained by Euphoria Development.
Contributions to code, tests and documentation are welcome. Panel changes belong
in [Aquadactyl/aquadactyl](https://github.com/Aquadactyl/aquadactyl).

## Choose the right channel

- Report vulnerabilities privately using [SECURITY.md](SECURITY.md).
- Use the [issue forms](https://github.com/Aquadactyl/manta/issues/new/choose) for
  reproducible bugs, requests and help after reading
  [Manta common issues](https://aquadactyl.uk/docs/manta/troubleshooting).
- Feature requests are welcome without prior approval. Discuss substantial changes
  in an issue first so maintainers can agree on scope.

Review logs and diagnostics before posting. Do not publish node configuration,
tokens, private keys, complete service environments or user data.

## Development and validation

Fork this repository and create a focused branch from `main`. Use the Go version
and toolchain in `go.mod` and the lint version in `.github/workflows/lint.yaml`.
On an isolated Linux development host with Docker available:

```sh
go mod download
go test ./...
go test -race ./...
golangci-lint run ./...
```

Linux CI also validates the release installer and service migration. The macOS
jobs build and test natively on Intel and Apple Silicon; Docker-dependent tests
run on Linux. Preserve platform-specific CI checks and add regression coverage
for changed behaviour, error handling and permissions. Keep public protocols,
existing configurations and operator data compatible or document a migration.

Operator documentation lives at
[aquadactyl.uk/docs/manta/installing-manta](https://aquadactyl.uk/docs/manta/installing-manta)
and in `docs/`. Update relevant guides when commands, configuration, paths or
installation behaviour change. Preserve upstream licence and copyright notices.

## Pull requests and review

Describe the problem, resulting behaviour, validation and deployment impact in
the PR template. Keep changes focused and respond to review comments. Merging
into `main` requires at least one approval from a maintainer listed in
[CODEOWNERS](.github/CODEOWNERS), all required CI checks and resolved review
conversations. New changes invalidate previous approvals, and the latest push
must be approved by someone other than its pusher. These requirements also apply
to administrators; direct pushes, force pushes and branch deletion are blocked.

Workflows from external forks require maintainer approval before running. Starting
CI is separate from approving a contribution. CI uses GitHub-hosted runners and
read-only default tokens. Never add secrets to a PR or switch untrusted builds
to `pull_request_target`.

Published `v*` release tags cannot be moved or deleted. New versions get new tags.
If a Go matrix version or job name changes, coordinate the matching required
status-check names in the repository rules before merging.

## AI-assisted contributions

You remain responsible for AI-assisted work. Understand and review every change,
verify its behaviour, and describe it in your own voice. The same code review,
testing, documentation and licensing requirements apply to all contributions.
