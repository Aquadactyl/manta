# Install and update Manta

![Manta](../.github/assets/manta-logo.png)

Manta is the Aquadactyl server daemon, maintained by [Euphoria Development](https://euphoriadevelopment.uk).
The current prerelease is **v1.1-beta.2**. Test upgrades on a staging node first.

## New Linux installation

Install Docker Engine using its [official instructions](https://docs.docker.com/engine/install/), then fetch the matching Manta release tools:

```bash
git clone --depth 1 --branch v1.1-beta.2 https://github.com/Aquadactyl/manta.git /opt/manta &&
cd /opt/manta &&
sudo bash scripts/install.sh v1.1-beta.2 &&
/usr/local/bin/manta version &&
sudo install -d -m 0700 /etc/aquadactyl &&
sudo install -d -m 0700 /var/lib/aquadactyl/volumes
```

### Existing or interrupted checkout

If `/opt/manta` already exists, use this block instead of cloning again:

```bash
cd /opt/manta &&
git fetch --no-tags --depth 1 https://github.com/Aquadactyl/manta.git refs/tags/v1.1-beta.2 &&
git checkout --detach FETCH_HEAD &&
test -f deploy/manta.service &&
sudo bash scripts/install.sh v1.1-beta.2 &&
/usr/local/bin/manta version
```

The fetch selects the published tag directly from the official repository. It does
not depend on an existing branch or local tag being current. Git preserves
untracked configuration files and refuses conflicting changes; review local
changes if checkout fails. Each `&&` stops the following commands on failure.

An installer argument such as `v1.1-beta.2` selects the binary to download; it does
not update the installer script itself. Running an older script can still install
that binary under its old executable name. Switch the checkout first. Continue
with configuration and service setup below, or use [Migrate the service](#migrate-the-service)
for an existing daemon.

The installer verifies the SHA256 checksum and executable version, then atomically installs `/usr/local/bin/manta`. Release assets are `manta_linux_amd64`, `manta_linux_arm64` and the corresponding `manta_darwin_*` files. Legacy asset aliases are supplied for older update tools.

Create a location and node in **Admin > Nodes**, with a public hostname, trusted TLS certificate or TLS reverse proxy, and allocations for game ports. The new node's data directory is `/var/lib/aquadactyl/volumes`. Save the generated configuration to `/etc/aquadactyl/config.yml` and protect it:

```bash
sudo chmod 0600 /etc/aquadactyl/config.yml
```

New Panel configurations include `config_version: 1`. Keep this line when writing a new configuration by hand: it selects the Aquadactyl defaults. Older files without this marker keep their original inherited defaults when loaded, even if copied to the new configuration directory.

| New Linux default | Value |
| --- | --- |
| Executable | `/usr/local/bin/manta` |
| Systemd unit | `/etc/systemd/system/manta.service` |
| Configuration | `/etc/aquadactyl/config.yml` |
| Data and state | `/var/lib/aquadactyl` |
| Server files | `/var/lib/aquadactyl/volumes` |
| Archives / backups | `/var/lib/aquadactyl/archives` / `/var/lib/aquadactyl/backups` |
| Log | `/var/log/aquadactyl/manta.log` |
| Temporary / runtime files | `/tmp/aquadactyl` / `/run/manta` |
| Game-file owner | `aquadactyl` |
| Docker network / bridge | `aquadactyl_nw` / `aquadactyl0` |

## Start the Manta service

The release includes `deploy/manta.service`. For a new, root-run Linux node:

```bash
sudo test -r /etc/aquadactyl/config.yml &&
sudo install -m 0644 /opt/manta/deploy/manta.service /etc/systemd/system/manta.service &&
sudo systemctl daemon-reload &&
sudo systemctl enable --now manta &&
sudo systemctl status manta --no-pager &&
sudo journalctl -u manta -n 50 --no-pager
```

The unit runs `/usr/local/bin/manta` and allows normal configuration discovery. Only one daemon service should run on a node. Existing services with custom users, flags, limits or Docker dependencies should follow the migration steps below.

Check node connectivity and a test server's install, start, console, files, SFTP and backup. Expose only the API, SFTP and required game ports, keep Docker's socket private, and configure trusted HTTPS. For SELinux hosts see the README. For macOS use the [macOS guide](macos.md).

## Update

Back up configuration and server data. From the reviewed tools checkout:

```bash
cd /opt/manta &&
git fetch --no-tags --depth 1 https://github.com/Aquadactyl/manta.git refs/tags/v1.1-beta.2 &&
git checkout --detach FETCH_HEAD &&
sudo bash scripts/install.sh v1.1-beta.2 &&
sudo systemctl status manta --no-pager &&
sudo journalctl -u manta -n 50 --no-pager
```

The installer retains `manta.previous` and restarts an active service. For an existing legacy service it also updates the old executable, preserves its previous copy, and reports the remaining service migration. It never creates the old executable on a fresh install. A failed service start restores the previous binaries. Check logs and panel connectivity after systemd accepts the start request; later runtime errors still require operator attention.

Consoles and SFTP briefly disconnect during a daemon restart. Existing game containers keep running. Schedule updates outside installations, transfers and backups. For offline installations, download the matching binary and `checksums.txt`, then pass `--from-directory /path/to/downloads`.

## Migrate the service

For nodes previously installed as Wings, first update the executable using the verified installer above. Back up the existing configuration and unit in a protected directory:

```bash
sudo install -d -m 0700 /var/backups/manta
sudo systemctl cat wings | sudo tee /var/backups/manta/wings.service >/dev/null
sudo cp --preserve=mode,ownership --no-clobber /etc/pterodactyl/config.yml /var/backups/manta/config.yml
sudo install -d -m 0700 /etc/aquadactyl
sudo cp --preserve=mode,ownership --no-clobber /etc/pterodactyl/config.yml /etc/aquadactyl/config.yml
sudo install -m 0644 /opt/manta/deploy/manta.service /etc/systemd/system/manta.service
```

These commands describe a standard legacy config location; use the path in your existing unit if different. If an Aquadactyl file already exists, review and use it; the copy does not overwrite it. Preserve its tokens, certificates, local paths and user. **Do not add the new defaults marker to an old partial config**: leaving it absent preserves omitted legacy values. Manta writes those values explicitly when it next saves the file.

Compare the backed-up unit with `manta.service` before switching. Carry over custom flags, service users, dependencies and limits with `sudo systemctl edit --full manta`; change the executable to `/usr/local/bin/manta` and explicit configuration arguments to the intended file. Match directory/file ownership to a rootless service user where needed.

```bash
sudo systemctl disable --now wings
sudo systemctl daemon-reload
sudo systemctl enable --now manta
sudo systemctl status manta --no-pager
sudo journalctl -u manta -n 50 --no-pager
```

The retired unit and executable can be removed after checking console access, SFTP, backups and a test server. Keep the backups for rollback. This migration changes the service name and config location; it deliberately leaves existing game-file locations and UIDs intact.

## Existing data and Docker deployments

Existing explicit paths, users and network names remain valid. Old unversioned configs retain their omitted legacy defaults, and an existing `wings.db` is reused rather than replaced with an empty activity database. New nodes create `manta.db`.

To relocate existing game data, schedule maintenance, stop all affected game containers and daemon operations, make a verified backup, copy the data with ownership/ACLs preserved, and update both the node's data directory in the panel and `system.data` in the full local config. Existing Docker bind mounts must be recreated to reference the new host path before starting servers. Renaming a directory under running containers is insufficient. There is no automatic bulk data move during an update.

The new Docker example uses `manta`, `MANTA_UID`, `MANTA_GID` and `MANTA_USERNAME` with Aquadactyl mounts. For an existing node retain every host data/certificate/runtime mount required by its config until separately migrating the paths. The container must see bind-mounted game data at the same absolute path as Docker's host. `MANTA_*` environment overrides take precedence over the corresponding legacy `WINGS_*` variables.

## Configuration location and existing nodes

Without an explicit `--config` or `--config-path`, Manta discovers `/etc/pterodactyl/config.yml` only if `/etc/aquadactyl/config.yml` is absent. The Aquadactyl file takes precedence when both exist. Invalid or unreadable canonical files fail with their own error, rather than loading another node's credentials. Explicit paths are always honored.

## Rollback

Stop `manta`, restore `manta.previous` to `/usr/local/bin/manta`, and restart it. To restore a retired legacy service, stop and disable `manta` before restoring the old binary/config and enabling the old unit. Never run both services together. Reverting a binary does not undo configuration or panel database changes.

## Maintainer publication

Dispatch the Release workflow for the reviewed source. Verify native Linux/macOS CI, checksums, source version and executable output. Its artifact contains four canonical binaries, four compatibility aliases, `checksums.txt` and `RELEASE_CHANGELOG`. Create and verify a draft prerelease before publishing it; build artifacts alone do not publish a release. Keep published tags immutable.
