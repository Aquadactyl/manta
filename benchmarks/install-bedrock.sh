#!/bin/ash
set -eu
cd /mnt/server
curl -fsS http://benchmark-assets:8000/bedrock.tar | tar xf -
chmod +x bedrock_server
cat > server.properties <<EOF
server-name=Manta Wings Benchmark
gamemode=creative
difficulty=peaceful
allow-cheats=true
online-mode=false
server-port=${SERVER_PORT}
server-portv6=$((SERVER_PORT+1))
level-name=benchworld
level-seed=746574
view-distance=4
tick-distance=4
max-threads=2
max-players=20
emit-server-telemetry=false
EOF
printf 'Benchmark Bedrock installation complete.\n'
