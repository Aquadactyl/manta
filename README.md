# Manta

Manta is a Linux server daemon for Aquadactyl Panel, based on Pterodactyl Wings. It manages Docker game servers, exposes the Wings HTTP and WebSocket APIs, provides SFTP access, and handles files, backups, installations, and transfers.

The existing Go module path, API routes, authentication format, and configuration paths are retained for compatibility. Aquadactyl integration should be verified against your Panel version before replacing a running daemon.

## Performance and security

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

## Build and verify

Use Linux and the Go toolchain selected by `go.mod`:

```sh
go build -trimpath -o build/manta .
go test ./...
go test -race ./...
go vet ./...
```

Run with the existing node configuration:

```sh
./build/manta --config /etc/pterodactyl/config.yml
```

The Dockerfile also builds the daemon. Existing deployment assets retain the `wings` executable name.

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
