# Local game-server comparison

Read [REPORT.md](REPORT.md) for the measured results and limitations. Raw data and derived tables are in [results/](results/).

The [performance follow-up](OPTIMIZATION.md) compares targeted request/event optimizations with the previous Manta build. Its raw data are under `results/optimization/`; rebuild that summary with `python benchmarks/summarize_optimization.py`. The original Wings results remain separate.

The next [lookup and allocation pass](SCALABILITY.md) adds a UUID index and deferred request-logger construction. It measures 1-1,000 in-memory registrations, plus the existing live game workload; it does not provision 1,000 games. Its raw data are under `results/scalability/`; rebuild it with `python benchmarks/summarize_scalability.py`.

The latest [stats and console pass](TELEMETRY.md) reduces periodic resource-decoding allocations and running-console inspection work. Its raw data are under `results/telemetry/`; rebuild it with `python benchmarks/summarize_telemetry.py`. `check-telemetry.php` checks authenticated resource reporting through the local Panel without printing credentials.

The existing benchmark stack contains two dedicated nodes, four game servers, matching cached game binaries, and local installer eggs. It leaves the original user servers outside the measured requests. All provisioned servers are owned by the existing `repgraphics` administrator.

| Component                   | Docker name                                              |
| --------------------------- | -------------------------------------------------------- |
| Manta                       | manta-test                                               |
| Stock Wings                 | wings-benchmark                                          |
| Internal cached game assets | manta-benchmark-assets                                   |
| Stock config/data volumes   | manta-benchmark-wings_config, manta-benchmark-wings_data |
| Stock game network          | manta-benchmark-wings_servers                            |
| Cached binaries volume      | manta-benchmark_assets                                   |

## Repeating the measurements

With the existing local Aquadactyl test stack running, apply the same daemon resource settings:

```powershell
docker compose -f compose.local.yaml -f benchmarks/compose.manta.yaml up -d --no-build --wait
docker compose -f benchmarks/compose.yaml up -d --wait
.\benchmarks\Run-Benchmark.ps1
python benchmarks/summarize.py
```

Use `-Phases performance,security` to repeat selected phases. The runner can start/stop only servers whose benchmark manifest has the expected node keys, two servers per node, and `Benchmark` name prefix. `provision.php` additionally selects only servers with `manta-benchmark-` external IDs and refuses non-local Panels. Runtime credentials are held in a temporary file and removed in a `finally` block. Do not log that manifest.

Use `-OutputDirectory benchmarks/results/my-run` to preserve earlier measurements. The default output directory remains `benchmarks/results`.

The full run starts/stops the four test servers, writes fixtures under their `bench/` directories, and runs a short console block-update workload. Repeating lifecycle tests on existing worlds measures cached restarts; the `cold` row then means the first start of that run and no longer represents first world creation. Raw data will be replaced. Preserve the current results before rerunning if you want a historical comparison.

`Run-Benchmark.ps1` assumes the benchmark nodes, cached assets, cloned installer eggs, images, and four servers have already been provisioned. The setup sources retained here are `provision.php`, the two install scripts, the stock Compose file, and `wings-proxy.conf`. The fixed source server IDs in the recorded setup belong to this user's existing local cache; this is not a generic fresh-machine installer.

After recreating the Panel container, restore its loopback relays with `docker/Start-LocalManta.ps1 -SkipBuild` and copy `benchmarks/wings-proxy.conf` to `/etc/nginx/http.d/local-wings-benchmark.conf` inside the Panel, followed by `nginx -t` and `nginx -s reload`. Reapply `compose.manta.yaml` afterward because the normal start script uses the normal, unconstrained local Compose settings.

The Panel's SFTP compatibility fix is in the Aquadactyl repository and has been copied into the running test Panel. A future Panel image rebuild from that repository includes it. The Manta image includes the new full-UUID/modern-identifier username support.

## What is covered

Install containers and Panel status callbacks, game startup/shutdown, API concurrency, directory listing, HTTP/SFTP transfers with integrity checks, archive round trips, log fanout, idle/load CPU and working memory, game queries and Paper tick reports, and 16 live hardening/compatibility checks per daemon.

The full Go race suite also covers cancellation races, bounded pagination, redirect credential protection, Panel response limits/read failures, token expiry retention, and the other source-level protections. The benchmark does not run authenticated player bots or a production endurance test.
