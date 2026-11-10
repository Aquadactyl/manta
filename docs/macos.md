# Running Manta on macOS

Manta runs natively on macOS, on both Apple Silicon (`arm64`) and Intel
(`amd64`). It manages game servers the same way it does on Linux, but macOS has
no container support of its own: the containers run in the Linux virtual machine
of Docker Desktop, OrbStack, Colima or Rancher Desktop, and Manta talks to the
Docker engine inside it.

This makes macOS a good fit for a development node or a small host. For a
production node under load, Linux gives the containers direct access to the files
and the kernel and remains the recommended platform.

## What is different from Linux

| Area            | Linux                         | macOS                                                |
| :-------------- | :---------------------------- | :--------------------------------------------------- |
| Configuration   | `/etc/pterodactyl/config.yml` | `~/Library/Application Support/Manta/config.yml`     |
| Data and state  | `/var/lib/pterodactyl`        | `~/Library/Application Support/Manta`                |
| Logs            | `/var/log/pterodactyl`        | `~/Library/Logs/Manta`                               |
| Temporary files | `/tmp/pterodactyl`            | `~/Library/Application Support/Manta/tmp`            |
| Server user     | A `pterodactyl` system user   | The user Manta runs as, nothing is created           |
| Service manager | systemd                       | launchd                                              |
| Docker socket   | `/var/run/docker.sock`        | `/var/run/docker.sock`, or the socket of the runtime |
| Host metrics    | CPU, RAM and disk             | RAM and disk. CPU is always `null`                   |

Every path can still be set in the configuration file, and values set there
always win over the defaults above.

## Requirements

- macOS 13 or newer.
- A Docker runtime with its engine running, for example [Docker Desktop](https://www.docker.com/products/docker-desktop/), [OrbStack](https://orbstack.dev), [Colima](https://github.com/abiosoft/colima) or Rancher Desktop.
- An Aquadactyl Panel with a node created for this machine.

### Docker socket

Manta connects to the engine in this order:

1. `DOCKER_HOST`, if set.
2. `/var/run/docker.sock`. Docker Desktop and OrbStack create it by default.
3. The socket in the home directory: `~/.docker/run/docker.sock` (Docker Desktop), `~/.orbstack/run/docker.sock`, `~/.colima/default/docker.sock` or `~/.rd/docker.sock`.

### File sharing

The containers reach server files through the file sharing of the runtime, so
the **server data directory must be in a location the runtime shares, and
shares writable**. The default, inside your home directory, is shared by Docker
Desktop and OrbStack. Colima and Rancher Desktop mount the home directory
read-only unless told otherwise, so make the mount writable in their
configuration (for Colima, `writable: true` on the home mount) or Manta will be
unable to start servers. If you move the data to an external disk or a path
outside of `/Users`, add it to the file sharing settings of your runtime first.
The directory is set in the panel under the node's settings.

## Installation

1. Download the binary for your Mac and its checksum file from the
   [releases page](https://github.com/Aquadactyl/manta/releases). Use
   `wings_darwin_arm64` on Apple Silicon and `wings_darwin_amd64` on Intel.

   ```sh
   MANTA_VERSION="v0.3-beta"
   ARCH="$(uname -m | sed 's/x86_64/amd64/')"

   curl -fL -o wings "https://github.com/Aquadactyl/manta/releases/download/${MANTA_VERSION}/wings_darwin_${ARCH}"
   curl -fL -o checksums.txt "https://github.com/Aquadactyl/manta/releases/download/${MANTA_VERSION}/checksums.txt"
   grep "wings_darwin_${ARCH}" checksums.txt
   shasum -a 256 wings
   ```

   The two hashes must match.

2. Install it:

   ```sh
   sudo mkdir -p /usr/local/bin
   sudo install -m 0755 wings /usr/local/bin/wings
   wings version
   ```

3. In the panel, create the node and copy its configuration to
   `~/Library/Application Support/Manta/config.yml`, or run
   `wings configure` and answer the prompts. The directory is created for you.

   Set the node's data directory to a path that is shared with the Docker
   runtime, such as `/Users/<you>/Library/Application Support/Manta/volumes`.

4. Run it:

   ```sh
   wings
   ```

   A green heart next to the node in the panel means it is connected.

## Running at login with launchd

Install the LaunchAgent so Manta starts when you log in and is restarted if it
stops. It has to run as your user, because that user owns the Docker runtime:

```sh
mkdir -p ~/Library/LaunchAgents ~/Library/Logs/Manta
sed "s|__HOME__|$HOME|g" macos/uk.aquadactyl.manta.plist \
  > ~/Library/LaunchAgents/uk.aquadactyl.manta.plist
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/uk.aquadactyl.manta.plist
```

Common commands:

```sh
launchctl print "gui/$(id -u)/uk.aquadactyl.manta"        # status
launchctl kickstart -k "gui/$(id -u)/uk.aquadactyl.manta" # restart
launchctl bootout "gui/$(id -u)/uk.aquadactyl.manta"      # stop and unload
```

A LaunchAgent only runs while the user is logged in. On a machine that has to
come back after a reboot without anyone logging in, enable automatic login for
that user and start the Docker runtime at login too.

## Notes and limits

- **Servers run as your user.** Containers see the files through the file
  sharing of the runtime, which presents them as owned by the user running
  Manta. Manta does not create a system user, and ownership of server files is
  not changed to a separate one.
- **Performance.** File access from a container goes through the file sharing
  layer, which is slower than on Linux. Large worlds, many small files and
  backups feel it most.
- **Path length.** The kernel reports a path of at most 1024 bytes when Manta
  checks that a file is inside the server directory, so deeper paths are
  refused instead of risking an escape.
- **Case-insensitive volumes.** The default macOS volume ignores case in file
  names. The containment check compares exactly what the kernel returns, which
  can only refuse a path, never allow one it should not.
- **Log rotation.** Manta rotates `~/Library/Logs/Manta/wings.log` itself at
  10 MiB and keeps seven old files (`wings.log.1` to `wings.log.7`). Set
  `system.enable_log_rotate: false` to turn this off. Manta then reopens the file
  on `SIGHUP` for a tool that rotates it from the outside, which you can send
  with `launchctl kill SIGHUP "gui/$(id -u)/uk.aquadactyl.manta"`.
- **CPU burst** and the other cgroup based features only apply on Linux.
- **Gatekeeper.** Binaries downloaded with `curl` are not quarantined and need
  no approval. If you download one with a browser, run
  `xattr -d com.apple.quarantine wings` once.

## Building from source

```sh
go build -trimpath -o build/manta .
```

Cross-compile for macOS from any host with `GOOS=darwin GOARCH=arm64` (or
`amd64`) and `CGO_ENABLED=0`.
