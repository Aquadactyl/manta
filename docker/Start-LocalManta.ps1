param(
    [string]$PanelContainer = 'aquadactyl-test-panel-1',
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$workspacePath = Split-Path -Parent $PSScriptRoot
$composePath = Join-Path $workspacePath 'compose.local.yaml'

function Invoke-LocalDocker {
    & docker @args
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed with exit code $LASTEXITCODE."
    }
}

$panel = (Invoke-LocalDocker inspect $PanelContainer | ConvertFrom-Json)[0]
if (!$panel.State.Running) { throw 'The Aquadactyl test Panel must be running first.' }
Invoke-LocalDocker network inspect aquadactyl-test_default | Out-Null

if (!$SkipBuild) {
    Invoke-LocalDocker build --tag manta:local-test --build-arg VERSION=1.13.3-manta-local --file (Join-Path $workspacePath 'Dockerfile') $workspacePath
}

Invoke-LocalDocker volume create manta-test_config | Out-Null
Invoke-LocalDocker volume create manta-test_data | Out-Null
$dataVolume = (Invoke-LocalDocker volume inspect manta-test_data | ConvertFrom-Json)[0]
$env:MANTA_DATA_PATH = $dataVolume.Mountpoint
$networkNames = @(Invoke-LocalDocker network ls --format '{{.Name}}')
if ('manta-test_servers' -notin $networkNames) {
    Invoke-LocalDocker network create --driver bridge manta-test_servers | Out-Null
}
$serverNetwork = (Invoke-LocalDocker network inspect manta-test_servers | ConvertFrom-Json)[0]
$ipv4 = @($serverNetwork.IPAM.Config | Where-Object { $_.Subnet -notmatch ':' })[0]

$bootstrap = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'local-node.php') -Raw
$response = $bootstrap | & docker exec -i -w /app `
    --env "MANTA_DATA_ROOT=$($dataVolume.Mountpoint)" `
    --env "MANTA_NETWORK_SUBNET=$($ipv4.Subnet)" `
    --env "MANTA_NETWORK_GATEWAY=$($ipv4.Gateway)" `
    $PanelContainer php
if ($LASTEXITCODE -ne 0) { throw 'Could not create or read the local Manta node.' }
$node = $response | ConvertFrom-Json
$node.configuration | & docker run --rm -i --mount type=volume,source=manta-test_config,target=/config `
    busybox:1.37.0-musl sh -c 'umask 077; cat > /config/config.yml'
if ($LASTEXITCODE -ne 0) { throw 'Could not save the node configuration.' }
$node.configuration = $null
$response = $null

Invoke-LocalDocker compose --file $composePath up --detach --build --force-recreate --wait --wait-timeout 120
# Install the same local loopback relay used for the existing Wings node. This
# reloads nginx without recreating the Panel container or touching its servers.
Invoke-LocalDocker cp (Join-Path $PSScriptRoot 'local-panel-proxy.conf') "${PanelContainer}:/etc/nginx/http.d/local-manta.conf"
Invoke-LocalDocker exec $PanelContainer nginx -t
Invoke-LocalDocker exec $PanelContainer nginx -s reload
$verification = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'check-local-node.php') -Raw |
    & docker exec -i -w /app $PanelContainer php
if ($LASTEXITCODE -ne 0) { throw 'Manta started, but the authenticated Panel connection check failed.' }
$verifiedNode = $verification | ConvertFrom-Json
Write-Output "Local Manta is running and registered as node $($node.node_id)."
Write-Output "Panel: $($node.panel_url)/admin/nodes/view/$($node.node_id)"
Write-Output 'API: http://manta.localhost:8083; SFTP: localhost:2023; game allocations: 27015-27019.'
Write-Output "Authenticated Panel connection verified: $($verifiedNode.version)."
