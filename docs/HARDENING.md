# Daemon resource bounds and operation reliability

This unreleased follow-up was developed against revision
`59fd2a957ab2bcc74c1040c0cf714d09dd31a3cc` on 23 November 2026. It keeps the
existing HTTP routes, JSON formats, WebSocket events, SFTP protocol and on-disk
configuration. Installation instructions continue to target published releases.

## WebSocket control messages

- Retain the existing 4-KiB compressed wire-message limit and enforce the
  existing 32-KiB decoded ceiling while reading. Oversized messages close with
  WebSocket code 1009. They are not fully decompressed into an unbounded buffer.
- Allow at most four concurrently executing inbound handlers per connection.
  Overflow uses the existing `throttled` event with scope `global`. Accepted power
  operations retain their server lifecycle semantics; closing a browser does not
  undo an accepted server power action.
- Track at most 512 pending connections per router and 64 per client IP. Admission
  is released on authentication, failed upgrade, timeout or disconnect. Excess
  attempts receive HTTP 429 with `Retry-After: 1`. The existing authenticated
  limit remains 30 per server, independent of pending connections, with the
  existing 30-second authentication timeout.
- Limit both rate buckets and throttle-notification state to the fixed event
  groups. Unrecognized event names share the default group. Concurrent initial
  lookups reuse the same bucket instead of resetting its tokens.
- Cancel Docker console-log lookup when the WebSocket disconnects, including
  requests waiting for headers and reads waiting for a response body. The
  WebSocket's five-second lookup deadline now covers the log stream too. Retain
  the existing `Readlog` interface; integrations can optionally implement
  `ReadlogContext`, and older providers keep their own cancellation behaviour.

Client IPs follow Gin's configured trusted-proxy handling. Configure only actual
proxies in `api.trusted_proxies` so forwarded client addresses have the intended
meaning. These admission limits are node control-channel bounds, not estimates
of game/player capacity.

## Downloads, archives and backups

Remote download admission now checks capacity and inserts its reservation under
one lock. The existing three-download per-server limit also holds for concurrent
requests. Completion/cancellation releases capacity. Internal callers retain the
existing unrestricted `downloader.New` function; the user-facing route uses
`NewWithLimit`.

Tar and gzip writer finalization errors propagate to callers. An operation whose
last buffered writes fail is reported as failed. Earlier traversal, cancellation
or write errors retain precedence while both writers are still closed. Existing
compression quota checks and partial-file cleanup remain in place.

Transfer destination acknowledgements are limited to 1 MiB, including responses
without a declared length. Oversized responses fail and close their bodies; the
uploaded archive itself retains its existing streaming path and size behaviour.
S3 upload-URL requests now inherit backup cancellation instead of continuing on
an independent background context.

Selected archive paths are indexed once and checked by exact path/ancestor
membership. Literal punctuation, trailing directory slashes, component boundaries
and existing absolute source-path handling remain compatible. This adds a small
operation-local lookup map; it does not cache file contents or change compression
levels. See the [matched measurements](../benchmarks/ARCHIVE-SELECTION.md).

## Validation scope

Regression tests exercise compressed-message boundaries before/after login,
pending admission and release, slow-handler bounds, disconnect cancellation,
concurrent bucket initialization, unknown event names, concurrent download
admission, archive final-flush failures and complete gzip/tar round trips,
bounded transfer responses and S3 cancellation. The archive final-flush test also
reproduces the lost error against the baseline implementation.

Validation includes full Linux tests, race-instrumented tests, vet, lint and
dependency reachability scanning. Native macOS and both CPU architectures remain
part of CI. `govulncheck` v1.10.0 is pinned in the existing required lint job;
findings are not suppressed. The local scan using Go 1.27.4 on this date reported
no known reachable vulnerabilities with the vulnerability database it retrieved.
The scan's database snapshot was last updated on 11 November 2026; results can
change when new advisories are published.

This review focused on control-message/resource bounds, download admission,
filesystem/archive operations, cancellation, transfer responses and dependencies.
Passing tests and a clean dependency scan do not establish that every possible
vulnerability has been found. Continue reporting security concerns privately
using [SECURITY.md](../SECURITY.md).
