# Manta coverage audit

2 November 2026. **The 100% statement-coverage target is not met.** The complete
Go-module audit covers **7,565 of 8,250 statements (91.696970%)**, leaving **685
statements uncovered**. No source files or packages were excluded to raise this
percentage. The CLI and benchmark runner are included.

All **823 Go cases/subtests in 28 packages** passed with race detection, with zero
failures and skips; `go vet ./...` passed. Five tests for the coverage auditor
also passed. A passing test suite and a successful coverage target are separate
results.

## Reproduce

From PowerShell, with Docker supporting privileged Linux containers and Python 3
installed:

```powershell
.\benchmarks\Run-Verification.ps1 -Python python
```

This starts an isolated Docker engine for tests that create containers/networks,
without mounting the workstation Docker socket into the Go test runner. The
engine, its temporary volumes, and its network are removed afterward. Results
are written to `build/verification/`. The script exits unsuccessfully when tests
or vet fail, any case is skipped, or the exact 100% target is unmet.

The measured command was:

```sh
go test -race -json -count=1 -coverpkg=./... -covermode=atomic -coverprofile=coverage.out ./...
go vet ./...
python benchmarks/coverage_report.py coverage.out --output coverage.json --minimum 100
```

Toolchain: Go 1.26.9 Linux amd64. Test engine: Docker 29.9.2 in a dedicated
Docker-in-Docker container. Cross-package blocks are merged by source location;
duplicate records do not increase the statement denominator. The target check
uses exact counts, so display rounding cannot turn an incomplete result into
100%.

## Remaining work

The largest gaps are the router (150 statements), CLI (126), filesystem (64),
Docker environment (54), and low-level Unix filesystem (47). Other gaps include
configuration, transfer/backup error paths, and SFTP lifecycle handling. Some
defensive error returns are unreachable for their current concrete input types;
they are retained in the denominator. This audit does not delete protections,
edit coverage counters, or redefine the measured scope to claim completion.

The [machine-readable report](results/verification-20261102/coverage.json) lists
every source file, its covered/total statements, and exact uncovered block
spans. The [validation record](results/verification-20261102/validation.json)
records test counts, command, toolchain, and the full profile/log SHA-256 values.
The website publication includes the original, unfiltered profile compressed
with gzip so the counts can be independently checked with Go's coverage tool.

## Changes checked in this run

- Malformed/truncated Docker inspect responses and malformed uptime values.
- Invalid atomic JSON values preserving the previous value, and zero-value
  atomics.
- Closing quota-wrapped files even when metadata reads fail, without changing
  tracked usage from an invalid size.
- Closing transfer HTTP responses on success, rejection, and cancellation. This
  exposed and fixed response-body leaks on early error returns.
- Deterministic completion of an asynchronous mock stop before changing its
  fields, fixing a race in the existing router test.
- Reusing the existing bridge in the root lifecycle test, avoiding network-name
  collisions when packages run concurrently.
