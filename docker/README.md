# Local Manta with Aquadactyl

This setup connects a separate Manta node to the existing `aquadactyl-test-panel-1` local Panel. It runs alongside the existing Wings node and uses its own Docker volumes and game network.

From the Manta workspace in PowerShell:

```powershell
.\docker\Start-LocalManta.ps1
```

The script builds this checkout, creates or reuses the **Local Manta** node and its credentials, saves configuration directly into a Docker volume, starts the daemon, and verifies an authenticated request from the Panel. It requires the existing Aquadactyl test stack and its `aquadactyl-test_default` Docker network. The local-only guard refuses to register against a production Panel.

| Item                 | Address or name                                                 |
| -------------------- | --------------------------------------------------------------- |
| Panel                | <http://localhost:8081>                                         |
| Node administration  | <http://localhost:8081/admin/nodes/view/2> on this installation |
| Node name            | Local Manta                                                     |
| API                  | <http://manta.localhost:8083>                                   |
| SFTP                 | localhost:2023                                                  |
| Game allocations     | localhost:27015-27019                                           |
| Container            | manta-test                                                      |
| Runtime image        | manta:local                                                     |
| Configuration volume | manta-test_config                                               |
| Data volume          | manta-test_data                                                 |
| Game network         | manta-test_servers                                              |

API, SFTP, and game allocations bind to host loopback. Node capacity is 4 GiB memory and 16 GiB disk, with no overallocation. It is a private node for deliberate local testing; select it manually when creating a server as an administrator. No sample game server is created automatically.

The configuration volume contains the generated node credentials. No credentials are written to the repository. Manta mounts the Docker socket to manage sibling game containers, matching the existing local Wings setup. The data volume is mounted at its real Docker host path so those sibling containers can access server files.

The script adds `/etc/nginx/http.d/local-manta.conf` inside the running Panel container and reloads nginx. This loopback relay is needed because the Panel's HTTP client resolves `manta.localhost` to its own container, while browsers resolve it to the Docker host. Run the setup script again after recreating the Panel container to restore the relay. The script retains the Manta node, token, and stored data on repeated runs, and recreates only the Manta daemon container.

Useful commands:

```powershell
docker compose --file compose.local.yaml ps
docker compose --file compose.local.yaml logs --tail 100 manta
docker compose --file compose.local.yaml stop
docker compose --file compose.local.yaml start --wait
```

If `manta:local-test` has already been rebuilt, use `-SkipBuild` to skip rebuilding the base image. Keep the existing local Wings daemon on ports 8082 and 2022.

Verified on 15 October 2026: Manta starts healthy, loads its server list from the Panel, the Panel retrieves Manta system information through the registered node address, unauthenticated API requests return 401, SFTP returns an SSH banner, and the existing Wings node remains reachable. Game installation, power actions, and authenticated SFTP are not covered by this connection check.

The subsequent [local benchmark](../benchmarks/REPORT.md) created Paper and Bedrock servers on Manta and a separate stock Wings node. It covers installation, power actions, authenticated SFTP, file/control workloads, and live hardening checks. Those four servers remain available in this Panel. See [benchmark instructions](../benchmarks/README.md) for resource settings and repeat runs.
