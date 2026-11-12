<!-- markdownlint-disable MD024 -->

# Changelog

Manta release notes.

This project follows [Semantic Versioning](http://semver.org) guidelines.

## Unreleased (Manta)

### Added

- Add native macOS support for Apple Silicon (`arm64`) and Intel (`amd64`). Manta manages containers through Docker Desktop, OrbStack, Colima or Rancher Desktop and runs unprivileged as the logged-in user. See [Running Manta on macOS](docs/macos.md).
- Add `wings_darwin_arm64` and `wings_darwin_amd64` binaries to the release workflow and include them in `checksums.txt`.
- Add a macOS CI job that mirrors the Linux one: Go 1.26.9 and 1.27.2 on both architectures, each built and tested natively (Apple Silicon on `macos-26`, Intel on `macos-26-intel`). It runs the filesystem, configuration and macOS specific tests, also under the race detector, and uploads the binaries as artifacts.
- Add built-in size based log rotation for macOS, which has no logrotate. `wings.log` rotates at 10 MiB and seven old files are kept, unless `system.enable_log_rotate` is `false`. Linux keeps using logrotate and reopening on `SIGHUP`.
- Add host CPU, RAM and disk metrics to `GET /api/system/usage` on macOS and advertise `system_usage` there. RAM comes from `hw.memsize` and the available share the kernel reports in `kern.memorystatus_level`, disk from the volume holding the server data, and CPU from the Mach host statistics through `purego`, so the binary stays a static build without cgo.
- Add a launchd LaunchAgent (`macos/uk.aquadactyl.manta.plist`) to start Manta at login and restart it if it stops, and a LaunchDaemon (`macos/uk.aquadactyl.manta.daemon.plist`) that starts it at boot as an ordinary user.
- Add a startup check on macOS (`docker.mount_check`, on by default) that runs a short container as the server user to find out whether the data directory is shared with the Docker runtime and writable, and says what to change when it is not, for example for the read-only home directory mount of Colima.
- Discover the Docker socket on macOS when `DOCKER_HOST` is unset and `/var/run/docker.sock` is missing, checking Docker Desktop, OrbStack, Colima and Rancher Desktop in order.
- Add `docker.image_pull_policy` to control when images are pulled for server containers and installation containers. `Always` (the default) keeps the current behavior of pulling every time and carrying on with a local copy if the pull fails, `IfNotPresent` pulls only when the image reference does not resolve to a local image, and `Never` fails with a clear error when the image is not present locally. Images prefixed with `~` are never pulled, as before.
- Add `docker.network.interfaces.v6.enabled` (default `true`) to turn IPv6 off on the Docker network Manta creates. IPv6 was always enabled before. It applies when the network is created, an existing network is left as it is.
- Take `allowed_mounts` from the Panel response when running `wings configure`. The data directory stays local, so a stale daemon base in the Panel can not move existing servers.

### Changed

- Split the Linux specific parts of `internal/ufs` into platform files. macOS resolves every path beneath its directory in userspace (`userspaceOpenBeneath`), one component at a time with every directory held open and every link read and checked instead of followed by the kernel, in place of `/proc/self/fd`. It enforces the same rules as `RESOLVE_BENEATH` (no `..`, absolute path or absolute link target leaving the directory, at most 40 links) and has no limit on the length or the case of a path. `openat2` stays Linux only, and `system.openat_mode` has no effect on macOS. The existing filesystem tests also run on this resolver on Linux (`UFS_TEST_USERSPACE_RESOLVE=1`).
- Use macOS locations for the default configuration, data, archive, backup, temporary, log and generated passwd directories. Defaults on Linux are unchanged and values set in the configuration file still take priority.
- Report the macOS version, build number and Darwin kernel version (for example `macOS 15.1 (24B83)`) from `GET /api/system` using `sysctl`, without `/etc/os-release` or `system_profiler`, so the panel can reach the node. The Darwin version from `uname` is used if the product version is unavailable.
- Detect the system timezone on macOS from `/etc/localtime`.
- Create the directory of the configuration file when it is missing before writing it.
- Skip the Linux only CPU burst cgroup update on other platforms instead of logging a warning for every container.
- Handle the SFTP sessions of a connection concurrently instead of one after another, and keep the connection open until all of them have finished. A failing session is logged and no longer ends the others.
- Print the version as `manta <version>` from `wings version`, without a second `v` in front of a version that already has one.
- Return an error from the configuration file parser for an unknown parser type instead of silently doing nothing.
- Stop building the JWT signer from an empty token in `config.Set`, and clear it when the token is removed.

### Fixed

- Run the descriptor leak regression test on every Unix platform by counting descriptors with `fcntl` instead of reading `/proc/self/fd`.
- Fix S3 backup part uploads sending a truncated body when a retry was needed. Every attempt now reads the part again from its own offset in the file, where a retry used to continue with what was left of the body of the failed attempt.
- Report a failure to notify the Panel of a successful backup with its own error, instead of returning an unrelated earlier error value.
- Honor exclusive create requests (`SSH_FXF_CREAT` with `SSH_FXF_EXCL`) over SFTP, so they fail with an error when the file exists instead of truncating it.

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
