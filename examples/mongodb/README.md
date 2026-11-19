# MongoDB 8.3 egg

This example fixes the MongoDB 8 egg's installer/runtime version mismatch and
its unbounded startup wait. It supplies an importable egg and a small runtime
image compatible with Aquadactyl and Manta.

The reported failure initialized data with `mongo:8-noble` (8.3.17), then
started `ghcr.io/ptero-eggs/yolks:mongodb_8` (8.2.11). The database contained
feature compatibility version 8.3, so the older runtime exited with code 62.
The egg ignored that exit and waited indefinitely for a closed port. Its `exit`
stop command could not be read until the MongoDB shell started.

## Build and import

Build on the node that runs the server:

```sh
docker build -t manta-mongodb:8.3.17 examples/mongodb
```

Import [egg-mongodb-8.3.json](egg-mongodb-8.3.json) into Aquadactyl. The runtime
image is `~manta-mongodb:8.3.17`; the `~` tells Manta to use the local image.
Build it on every node where this egg will run. It is not a published image.
Both the installer and runtime use MongoDB **8.3.17**. Updating this example
requires updating and testing both together.

The runtime runs as the server's configured user, keeps authentication enabled,
and supports Manta's read-only root filesystem and capability restrictions.
MongoDB runs in the foreground; startup errors go to the console and its real
exit status reaches Manta. Stop sends `SIGTERM` directly to MongoDB, allowing a
clean shutdown. The console displays MongoDB logs rather than a MongoDB shell;
use an authenticated MongoDB client for queries.

## Recover the reported existing server

1. Stop the server and take a backup or snapshot of its complete server directory.
2. Build the image above on that server's node.
3. Import the replacement egg and assign it to the existing server. Keep its
   existing server directory, allocation (port 2001 in the report), username,
   password, and `mongod.conf`. Select `~manta-mongodb:8.3.17` as its image.
4. Apply the replacement startup command and the egg's stop command `^SIGTERM`.
   Check that the startup detection is `Waiting for connections`.
5. Start the server and verify an authenticated connection on its assigned port.

Do **not** reinstall or delete `mongodb/` to fix this existing database. The
installer refuses to initialize a nonempty data directory. Switching runtime
versions preserves the data and existing credentials; editing the password
variable alone does not rotate an existing database password.

An unmodified official `mongo` image does not execute the egg's `STARTUP`
environment variable; use the accompanying runtime wrapper. The startup retains
the existing configuration, overrides the allocation/data path and console log
destination, enforces authentication, and disables background forking.

MongoDB documents version/FCV handling in its
[8.3 upgrade guidance](https://www.mongodb.com/docs/v8.3/release-notes/8.3-upgrade-standalone/).
The original egg is in
[pterodactyl/application-eggs](https://github.com/pterodactyl/application-eggs/blob/main/database/nosql/mongodb/egg-mongo-d-b8.json).

## Verification

From PowerShell with Docker running:

```powershell
./examples/mongodb/Test-MongoDB.ps1
```

The script creates a dedicated test volume, reproduces the 8.3/8.2 FCV failure,
checks authenticated writes and rejection of unauthenticated reads on port 2001
under Manta-like restrictions, verifies clean signal-based shutdown and data
survival across restart, ensures reinstall refuses existing data, and checks
that a bad configuration exits with an error. It publishes no ports and removes only
its own containers and test volume. It does not connect to a Panel or change any
existing game or database server.
