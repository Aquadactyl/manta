# Install and update Manta

![Manta](../.github/assets/manta-logo.png)

Manta is the Aquadactyl server daemon, maintained by
[Euphoria Development](https://euphoriadevelopment.uk).

Manta v1.1-beta.1 is a prerelease. Use a staging node before upgrading a production
fleet. Linux amd64 and arm64 releases are intended for server deployments;
[macOS](macos.md) supports local development and small hosts through a Docker VM.

## Linux

Install Docker Engine using the [official instructions](https://docs.docker.com/engine/install/),
then clone the matching release of Manta's tools:

```bash
git clone --depth 1 --branch v1.1-beta.1 https://github.com/Aquadactyl/manta.git /opt/manta
cd /opt/manta
sudo bash scripts/install.sh v1.1-beta.1
```

The installer selects amd64/arm64, verifies the release SHA256 and executable
version, and atomically installs `/usr/local/bin/wings`. This executable runs
Manta; Linux configuration now defaults to `/etc/aquadactyl/config.yml`.
Existing command/service names and server-data directories remain compatible.
Source builds use Go selected by `go.mod`: `go build -trimpath -o build/wings .`.

For a new node, create a location and node in **Admin > Nodes**, configure its
public HTTPS hostname and allocations, then save its generated configuration as
`/etc/aquadactyl/config.yml` (directory `0700`, file `0600`, owned by root). The
panel, browser and daemon must be able to reach each other's public addresses.
Configure a trusted certificate or TLS reverse proxy for the daemon; do not use
certificate-verification bypasses. Expose only the daemon, SFTP and required
game ports, keeping Docker's socket private.

Run `sudo /usr/local/bin/wings --config /etc/aquadactyl/config.yml` once to check
the configuration, then install this service at `/etc/systemd/system/wings.service`:

```ini
[Unit]
Description=Manta daemon
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
User=root
WorkingDirectory=/etc/aquadactyl
ExecStart=/usr/local/bin/wings --config /etc/aquadactyl/config.yml
Restart=on-failure
RestartSec=5
LimitNOFILE=4096
TimeoutStopSec=180

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now wings
sudo systemctl status wings --no-pager
```

Check node connectivity and an expendable test server's install, start, console,
files, SFTP and backup before opening access to users. The daemon requires root
on Linux and access to Docker, so install only trusted server images and eggs.
On SELinux hosts configure `docker.selinux_relabel` as described in the README.

## Configuration location and existing nodes

Since Manta v1.1-beta.1, new Linux nodes use `/etc/aquadactyl/config.yml`.
Without an explicit `--config` or `--config-path`, Manta can discover an existing
`/etc/pterodactyl/config.yml` when the new default is absent. If both exist,
the Aquadactyl file wins. An invalid or unreadable Aquadactyl file reports its
error rather than silently loading different credentials. Explicit paths are
always honored, including existing service arguments pointing at the old file.

To move a standard root-run Linux node to the new location, keep the original
file for rollback and copy its complete contents, including local paths and tokens:

```bash
sudo install -d -m 0700 /etc/aquadactyl
sudo cp --preserve=mode,ownership --no-clobber /etc/pterodactyl/config.yml /etc/aquadactyl/config.yml
sudo systemctl edit --full wings
```

In the service, set `WorkingDirectory=/etc/aquadactyl` and change any explicit
`--config` argument to `/etc/aquadactyl/config.yml`. Preserve the existing binary
path and any other options. Rootless services need directory and file ownership
that allows their configured service user to read them. Then apply and check it:

```bash
sudo systemctl daemon-reload
sudo systemctl restart wings
sudo systemctl status wings --no-pager
sudo journalctl -u wings -n 50 --no-pager
```

If an Aquadactyl configuration already exists, review and use it; the copy command
does not overwrite it. Game-server data directories are preserved by copying the
configuration rather than regenerating it. Keep the old file until the new
configuration and service have been verified.

For Docker, new deployments mount `/etc/aquadactyl` into the container. An existing
legacy bind mount continues to work through discovery when no explicit command
overrides it. Before switching a bind mount to the new directory, copy the full
configuration and preserve access to any certificate or other files it references.
Existing custom `command: --config ...` values must point at the intended file.

## Update or migrate from Wings

Back up `config.yml` and server data. Keep the previous binary and record its
version. From the reviewed Manta tools checkout:

```bash
git fetch origin tag v1.1-beta.1
git checkout --detach v1.1-beta.1
sudo bash scripts/install.sh v1.1-beta.1
sudo systemctl status wings --no-pager
sudo journalctl -u wings -n 50 --no-pager
```

The installer checks the download before stopping an active daemon, retains
`wings.previous`, and restarts an active service. A daemon restart briefly
disconnects consoles and SFTP; existing game containers keep running. Schedule
it outside installs, backups and transfers. A failed service start restores the
previous binary. Verify node health after every update; runtime configuration
errors may be reported after systemd accepts a start request.

To roll back, stop `wings`, copy `wings.previous` to `/usr/local/bin/wings`, then
start it and inspect its logs. Reverting the daemon binary does not roll back
panel migrations or configuration changes.

For an offline deployment, download the binary and `checksums.txt` from the same
release and pass `--from-directory /path/to/downloads`. Checksums detect download
corruption; trust in the GitHub release and maintainers is still required.

## Maintainer publication

Dispatch the **Release** workflow for the reviewed tag. It uploads a
`release-VERSION` artifact containing the four platform binaries,
`checksums.txt` and `RELEASE_CHANGELOG`. Verify checksums, the source version,
Linux/macOS CI and the binary's `version` output before publishing. A maintainer
can create the draft with `gh release create VERSION --verify-tag --draft
--prerelease --notes-file RELEASE_CHANGELOG wings_* checksums.txt`, then publish
with `gh release edit VERSION --draft=false --prerelease`. Build artifacts alone
do not create a public release. Keep published tags immutable.
