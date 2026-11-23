<p align="center"><img src=".github/assets/manta-logo.png" alt="Manta — Aquadactyl daemon" width="240"></p>

# Manta

Manta is the server daemon for [Aquadactyl Panel](https://aquadactyl.uk), maintained by [Euphoria Development](https://euphoriadevelopment.uk). It runs on Linux and, for development and small hosts, natively on macOS. It manages Docker game servers, provides HTTP and WebSocket APIs and SFTP access, and handles files, backups, installations, and transfers.

The API routes, authentication format and existing node interfaces retain Wings compatibility. The Go module is `github.com/Aquadactyl/manta`. Aquadactyl integration should be verified against your Panel version before replacing a running daemon.

On Linux the configuration defaults to `/etc/aquadactyl/config.yml`. Existing
`/etc/pterodactyl/config.yml` files are discovered only when the new default is
absent and no explicit configuration path was provided. The new path takes
precedence; explicit paths are always honored. See [configuration migration](docs/INSTALLING.md#configuration-location-and-existing-nodes).

New installs use the `manta` command, `manta.service`, `/var/lib/aquadactyl`,
`/var/log/aquadactyl`, `/tmp/aquadactyl` and the `aquadactyl` system user.
Existing nodes retain their configured data, user and network until explicitly migrated.

`manta diagnostics` prints a Manta report locally. Uploading is optional: pass
`--hastebin-url` with a Hastebin-compatible service you control, review the report
and confirm the upload. Manta has no default third-party diagnostics destination.

Beta tags now advance the minor version: `v1.3-beta`, followed by `v1.4-beta`.

v1.3-beta reduces directory-listing traversal and allocation work while retaining
root confinement and nonblocking inspection. See the [release notes](CHANGELOG.md#v13-beta)
and [matched component measurements](benchmarks/LISTING.md) for results and limits.

## Install v1.3-beta

```bash
git clone --depth 1 --branch v1.3-beta https://github.com/Aquadactyl/manta.git /opt/manta &&
cd /opt/manta &&
sudo bash scripts/install.sh v1.3-beta
```

If `/opt/manta` already exists, update its checkout first:

```bash
cd /opt/manta &&
git fetch --no-tags --depth 1 https://github.com/Aquadactyl/manta.git refs/tags/v1.3-beta &&
git checkout --detach FETCH_HEAD &&
sudo bash scripts/install.sh v1.3-beta &&
/usr/local/bin/manta version
```

Passing a release version to an old installer does not update that script.
The `&&` chains stop on errors, including a failed clone or checkout. See
[checkout recovery](docs/INSTALLING.md#existing-or-interrupted-checkout).

The Linux installer verifies the checksum and binary version, keeps the previous
executable, and restarts an existing active daemon service. Configuration and
server data stay in place. See the [installation and update guide](docs/INSTALLING.md)
for Docker, HTTPS, systemd, migration from Wings and rollback. macOS uses the
[macOS guide](docs/macos.md). This is a prerelease: test your workload before
updating production nodes.

## Performance and security

The latest unreleased [daemon hardening follow-up](docs/HARDENING.md) bounds
control-channel work and strengthens archive, transfer and cancellation handling.
Its selected-file archive measurements are separate from full backup throughput.

Manta includes filesystem containment, container restrictions, bounded SFTP and WebSocket connections, and checks against internal network destinations for user-requested downloads. The latest hardening adds:

- A 4 MiB limit for JSON control requests, including chunked bodies; file uploads, raw file writes, and transfers keep their existing streaming paths.
- Up to eight concurrent operations per rename, delete, or chmod request, stopping further scheduling after errors or request cancellation.
- Up to four concurrent Panel pagination requests during startup, stopping further scheduling after a failure.
- A 16 MiB limit for Panel API response bodies, including decompressed and chunked responses; read failures are propagated and the original body is closed.
- Request retries that reuse the serialized body without allocating another full copy.
- A redirect policy that keeps Panel credentials and SFTP authentication bodies on the original origin.
- Strict JWT expiry, issuance and not-before validation with 30 seconds of clock tolerance, and one-time token retention through the signed expiry.
- Synchronized remote download cancellation, including cancellation before execution, and cleanup of empty tracking entries.
- URL logs that omit credentials, query strings, and fragments, including upload/download tokens and signed URL parameters.

These bounds apply per request. They do not establish a global limit for simultaneous file operations or downloads. Local Paper/Bedrock benchmarks and Aquadactyl integration checks are linked below. Production workload testing is still needed to establish capacity and deployment suitability.

See the [hardening review](HARDENING.md) for benchmark results, regression coverage, and the remaining Docker dependency scan findings.

The 15 November release check passed 1,235 Go cases/subtests across 29 packages
with race detection and `go vet`. Full-module statement coverage measured
7,941/8,046 statements (98.70%). The 100% target remains unmet; the
[coverage audit](benchmarks/COVERAGE.md) retains its strict gate and commands for
recording every uncovered block. No source exclusions were added for this beta.

## Host resource metrics

On Linux, authenticated `GET /api/system/usage` returns host CPU utilization, used
and total RAM, and used and total space on the configured server-data filesystem.
`GET /api/system?v=2` advertises this through `capabilities.system_usage`. Missing
counters are `null`; idle usage remains zero. CPU uses a cancellable 200 ms sample,
and RAM usage excludes memory reported as available by the kernel.

On macOS the endpoint returns the same counters. RAM used is added up from the
Mach virtual memory counters the way Activity Monitor does (application memory
without purgeable pages, wired memory and compressed memory, so cached files are
not counted), and CPU is read from the Mach host statistics with a 200 ms sample,
the same as on Linux.

These counters describe the Linux host and filesystem visible to Manta, including
when it runs in Docker. They are distinct from game-container usage and assigned
server limits. The endpoint uses the same node-token authentication as the other
daemon APIs and does not return configuration, credentials or filesystem paths.

## Build and verify

Use Linux or macOS and the Go toolchain selected by `go.mod`:

```sh
go build -trimpath -o build/manta .
go test ./...
go test -race ./...
go vet ./...
```

Run with the existing node configuration:

```sh
./build/manta --config /etc/aquadactyl/config.yml
```

The Dockerfile also builds the daemon. Its entrypoint is `/usr/bin/manta`; a legacy executable alias supports existing container commands.

For macOS (Apple Silicon and Intel), see [Running Manta on macOS](docs/macos.md).

To run Manta alongside your existing local Aquadactyl Panel, see the [local Docker setup](docker/README.md).

Measured Paper/Bedrock results against stock Wings, live security checks, and reproduction instructions are in the [local benchmark report](benchmarks/REPORT.md).

The [performance follow-up](benchmarks/OPTIMIZATION.md) records the subsequent authentication, access-log, and console/event optimizations against the previous Manta build.

The [lookup and allocation comparison](benchmarks/SCALABILITY.md) measures UUID indexing and deferred request logging, including synthetic registration counts up to 1,000.

The [stats and console comparison](benchmarks/TELEMETRY.md) records decoder and running-console changes, resource-value parity checks, and live validation.

The [WebSocket event comparison](benchmarks/EVENTS.md) measures reduced per-viewer stats serialization and checks automatic stats delivery with 16 viewers on each benchmark game.

The [file browsing and streaming comparison](benchmarks/FILES.md) measures pooled content probes, directory JSON encoding, and larger streaming reads against the preceding Manta build, with live Docker controls.

The [resource-event comparison](benchmarks/RESOURCE_EVENTS.md) measures typed stats decoding and observer-aware periodic broadcasts, with disk-limit enforcement and API/console validation.

## Upstream

Manta derives from [Pterodactyl Wings](https://github.com/pterodactyl/wings), distributed under the [MIT license](LICENSE). The upstream [Wings documentation](https://docs.pterodactyl.io/v1/wings/) remains useful for the inherited configuration and API behavior.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) for bugs, feature requests, development and
pull request review. Report vulnerabilities privately using [SECURITY.md](SECURITY.md).
