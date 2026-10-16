#!/bin/ash
set -eu
cd /mnt/server
curl -fsS http://benchmark-assets:8000/paper.tar | tar xf -
cat > server.properties <<EOF
server-ip=0.0.0.0
server-port=${SERVER_PORT}
online-mode=false
enforce-secure-profile=false
level-name=benchworld
level-seed=746574
level-type=minecraft:flat
generate-structures=false
view-distance=4
simulation-distance=4
spawn-protection=0
allow-nether=false
max-players=20
pause-when-empty-seconds=-1
motd=Manta Wings Benchmark
EOF
printf 'eula=true\n' > eula.txt
printf 'Benchmark Paper installation complete.\n'
