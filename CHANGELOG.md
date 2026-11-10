<!-- markdownlint-disable MD024 -->

# Changelog

Manta release notes.

This project follows [Semantic Versioning](http://semver.org) guidelines.

## Unreleased (Manta)

### Added

- Add native macOS support for Apple Silicon (`arm64`) and Intel (`amd64`). Manta manages containers through Docker Desktop, OrbStack, Colima or Rancher Desktop and runs unprivileged as the logged-in user. See [Running Manta on macOS](docs/macos.md).
- Add `wings_darwin_arm64` and `wings_darwin_amd64` binaries to the release workflow and include them in `checksums.txt`.
- Add a macOS CI job that builds and vets both architectures, runs the filesystem, configuration and macOS specific tests under the race detector on a macOS runner, and smoke tests the binary.
- Add a launchd LaunchAgent (`macos/uk.aquadactyl.manta.plist`) to start Manta at login and restart it if it stops.
- Discover the Docker socket on macOS when `DOCKER_HOST` is unset and `/var/run/docker.sock` is missing, checking Docker Desktop, OrbStack, Colima and Rancher Desktop in order.

### Changed

- Split the Linux specific parts of `internal/ufs` into platform files. macOS opens paths with `openat` and verifies the canonical path of the resulting descriptor with `fcntl(F_GETPATH)` against the canonical base path, in place of `/proc/self/fd`, and refuses any path it cannot resolve. `openat2` stays Linux only and is never enabled elsewhere, including when `system.openat_mode` asks for it.
- Use macOS locations for the default configuration, data, archive, backup, temporary, log and generated passwd directories. Defaults on Linux are unchanged and values set in the configuration file still take priority.
- Report the macOS version, build number and Darwin kernel version (for example `macOS 15.1 (24B83)`) from `GET /api/system` using `sysctl`, without `/etc/os-release` or `system_profiler`, so the panel can reach the node. The Darwin version from `uname` is used if the product version is unavailable.
- Detect the system timezone on macOS from `/etc/localtime`.
- Create the directory of the configuration file when it is missing before writing it.
- Skip the Linux only CPU burst cgroup update on other platforms instead of logging a warning for every container.

### Fixed

- Run the descriptor leak regression test on every Unix platform by counting descriptors with `fcntl` instead of reading `/proc/self/fd`.

## v0.3-beta

### Added

- Add SELinux relabeling support for Docker bind mounts via the new `docker.selinux_relabel` configuration option (`shared` applies the `z` label for paths used by multiple containers, `private` applies `Z` for a single container). When set, server containers (`environment/docker/container.go`) and installation containers (`server/install.go`) receive their mounts as bind specifications through the new `environment.SELinuxBinds` helper, preserving read-only flags, and unsupported values are rejected with an explicit error. Leaving the option empty keeps the existing mount behavior.
- Add an isolated Docker verification workflow (`benchmarks/Run-Verification.ps1`) and an exact coverage auditing tool (`benchmarks/coverage_report.py`) that records every uncovered source block and fails explicitly until the 100% target is reached, documented in `benchmarks/COVERAGE.md`.
- Add regression tests for malformed and truncated Docker inspect responses, invalid inspect request paths, malformed container start times, atomic JSON handling, quota file close failures after `Stat()` errors, and transfer response body lifetime.
- Add Red Hat based Linux deployment notes to the README covering Rocky Linux, AlmaLinux, Docker Engine setup, and SELinux bind mount configuration.

### Changed

- Rename remaining project references from Wings to Manta across the CLI in `cmd/root.go`, including the command name, help text, version output, debug and `--auto-tls` flag descriptions, log messages, and the missing configuration notice, and reference Aquadactyl Panel instead of Pterodactyl Panel.
- Replace the Pterodactyl startup banner with a Manta logo and update the website, source, and license links to `aquadactyl.uk` and the `Aquadactyl/manta` repository.
- Run CI coverage collection with `-coverpkg=./...` so coverage is measured across the complete module rather than per package.
- Refresh the Paper/Bedrock benchmark comparison against stock Wings v1.13.4 and retain the results under `benchmarks/results/verification-20261102`, with all 14400 timed API requests completing without errors.

### Fixed

- Resolve a response body leak in `PushArchiveToTarget` (`server/transfer/source.go`) by closing the destination response body immediately after the request completes, instead of deferring the close inside the multipart branch.
- Eliminate a timing-dependent failure in the outgoing transfer route test by using a controlled HTTP receiver that keeps the first transfer active while duplicate rejection and cancellation are checked, and by waiting for transfer completion before cleanup.
- Resolve races and collisions in existing router and lifecycle tests, including an explicit Docker network name for the root command lifecycle test.

### Validation

- 823 race-detector test cases pass across 28 packages, with `go vet` passing.
- Complete module statement coverage of 7565/8250 statements (91.70%); the 100% coverage target remains unmet and its gate fails explicitly.
- 16 live Manta checks pass, and the isolated benchmark suite completes 31 cases with five samples each.
- The outgoing transfer route test passed 1000 consecutive runs under race detection and full-module coverage instrumentation at a two-core quota.

## v0.2-beta

### Added

- Add workspace VS Code configuration (`.vscode/settings.json`) targeting `GOOS=linux` for `gopls` language tools, eliminating false missing symbol warnings when developing on macOS or non-Linux host operating systems.

### Changed

- Modernize atomic concurrency primitives across `system` and `internal/progress` packages to Go 1.19+ typed atomics (`atomic.Bool`, `atomic.Uint64`, `atomic.Pointer[string]`, `atomic.Pointer[T]`), converting `AtomicBool` and `AtomicString` from `sync.RWMutex` to lock-free operations to eliminate lock contention on server state checks.
- Bind background installation, reinstallation, and power action goroutines in the router to `Server.Context()`, gracefully aborting operations and suppressing spurious error logs during server deletions and daemon shutdowns.
- Guard server installation state by setting `Server.SetInstalling(true)` during `Install()` and `Reinstall()` lifecycles to block conflicting power actions and transfers.
- Optimize streaming memory and throughput during archive creation and inter-daemon file transfers by increasing copy buffer sizes to 32KB/64KB and pooling buffers via `sync.Pool`, eliminating per-file heap slice allocations.

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
