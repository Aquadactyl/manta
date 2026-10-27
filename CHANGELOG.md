<!-- markdownlint-disable MD024 -->

# Changelog

Manta release notes.

This project follows [Semantic Versioning](http://semver.org) guidelines.

## Unreleased (Manta)

## v0.1-beta

### Security

- Resolve Server-Side Request Forgery (SSRF) vulnerabilities (CWE-918) in remote file pulls and remote backup restore endpoints by validating URL hostnames, restricting supported schemes to HTTP/HTTPS, and rejecting loopback, link-local, private, and cloud metadata network ranges.
- Prevent request forgery and unauthorized destination targeting (CWE-918) in inter-daemon server transfer pushes by validating and sanitizing target archive URLs.
- Deprecate `--ignore-certificate-errors` and remove insecure global TLS transport mutation (`InsecureSkipVerify: true`, CWE-295), causing daemon startup with this flag to fail fast with explicit operator guidance to configure valid CA certificates.
- Prevent path injection in low-level filesystem descriptors (CWE-022) by sanitizing path arguments passed to `os.NewFile` within the internal `ufs` package.
- Replace `ssh.InsecureIgnoreHostKey` with verified `HostKeyCallback` implementations in SFTP benchmarking utilities and connection tests (CWE-322).
- Enforce strict monotonic transfer state transitions in `server/transfer`, preventing unauthorized transitions from terminal or cancelling states (`StatusCancelling`, `StatusCancelled`, `StatusCompleted`, `StatusFailed`) back to pending or processing.

### Added

- Expose authenticated whole-machine host system resource usage endpoints (`/api/system`) covering overall CPU utilization, memory pressure, and root filesystem usage on Linux nodes for Panel monitoring.
- Support container timezone customization via user-configured `TZ` environment variables in `server/server.go`, allowing container timezones to override host defaults.
- Introduce configurable logrotate directory paths via `config.SetLogrotateDirectory` and automatic rootless mode detection, skipping privileged `/etc/logrotate.d` writes when running in unprivileged or containerized daemon environments.
- Add comprehensive benchmark suites under `benchmarks/runner` measuring concurrent WebSocket performance, SFTP throughput, and cgroup CPU burst overhead.

### Changed

- Modernize low-level directory streaming in `internal/ufs/walk_unix.go`, replacing deprecated `reflect.SliceHeader` structures with modern Go `unsafe.Slice` conversions for memory safety and zero allocations.
- Replace mutex locking in `usageLookupTime` (`server/filesystem/disk_space.go`) with lock-free `atomic.Pointer[time.Time]`, eliminating thread contention during concurrent disk quota lookups.
- Optimize string allocations and formatting across logging, configuration serialization, and environment parser routines using `fmt.Appendf` and consecutive buffer writes.
- Update GitHub Actions CI and release workflows to target the `main` branch with multi-architecture Linux container builds (`amd64`, `arm64`) and automated provenance.

### Removed

- Prune legacy Nix flake configuration files (`flake.nix`, `flake.lock`) and `.envrc` environment scripts.
- Remove obsolete speculative TODO comments and dead code paths across router, websocket, server, and filesystem packages.

### Fixed

- Eliminate host-specific container assumptions and hardcoded IDs (`82595161-eed8-4536-8f50-689b2c2912bb`) in Docker integration tests, dynamically provisioning and tearing down isolated test containers.
- Resolve permission errors on GitHub Actions runners when configuring log rotation by isolating test directories and respecting rootless execution.
- Scope `checkMountPathParents` root checks to isolated test directories (`mountPathCheckRoot`), preventing spurious parent directory ownership errors on unprivileged test runners.
- Resolve background data race between concurrent server installation and reinstallation routines in router integration tests by synchronizing on `InstallCompletedEvent`.

### Validation

- Full race-detector test suite (`go test -race ./...`) passes 100% across all 28 packages with zero failures.
- Zero static analysis and language server diagnostics reported by `gopls check`.
- Zero issues reported by `golangci-lint run`.
