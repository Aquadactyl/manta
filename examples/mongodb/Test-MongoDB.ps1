param([switch]$SkipBuild)

$ErrorActionPreference = 'Stop'
$egg = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'egg-mongodb-8.3.json') -Raw | ConvertFrom-Json
$image = ($egg.docker_images.PSObject.Properties | Select-Object -First 1).Value.TrimStart('~')
$prefix = 'manta-mongodb-' + [guid]::NewGuid().ToString('N')
$volume = "$prefix-data"
$createdContainers = @()
$volumeCreated = $false

function Invoke-MongoDocker {
    param([string[]]$DockerArguments, [int[]]$ExpectedExitCodes = @(0))
    $ErrorActionPreference = 'Continue'
    $output = & docker @DockerArguments 2>&1 | ForEach-Object { $_.ToString() }
    if ($LASTEXITCODE -notin $ExpectedExitCodes) { throw "Docker command failed ($LASTEXITCODE): $($DockerArguments[0])`n$($output -join "`n")" }
    $output
}

function Wait-MongoContainerExit {
    param([string]$Name, [int]$TimeoutSeconds = 30)
    $timer = [Diagnostics.Stopwatch]::StartNew()
    do {
        $state = (Invoke-MongoDocker @('inspect', '--format', '{{json .State}}', $Name)) | ConvertFrom-Json
        if (!$state.Running) { return $state }
        Start-Sleep -Milliseconds 200
    } while ($timer.Elapsed.TotalSeconds -lt $TimeoutSeconds)
    throw "Container $Name did not exit in $TimeoutSeconds seconds."
}

function Stop-MongoContainerGracefully {
    param([string]$Name)
    # This is the same Docker signal endpoint Manta uses for ^SIGTERM, without
    # docker stop's automatic SIGKILL fallback masking a failed graceful stop.
    Invoke-MongoDocker @('kill', '--signal', 'SIGTERM', $Name) | Out-Null
    $state = Wait-MongoContainerExit $Name
    if ($state.ExitCode -ne 0) { throw "MongoDB did not shut down cleanly: $($state.ExitCode)" }
    $logs = (Invoke-MongoDocker @('logs', $Name)) -join "`n"
    if ($logs -notmatch 'mongod shutdown complete') { throw 'MongoDB did not report a completed shutdown.' }
}

try {
    if (!$SkipBuild) {
        Invoke-MongoDocker @('build', '--tag', $image, $PSScriptRoot) | Out-Null
    }
    Invoke-MongoDocker @('pull', 'mongo:8.2.11') | Out-Null
    Invoke-MongoDocker @('volume', 'create', $volume) | Out-Null
    $volumeCreated = $true
    $password = 'smoke password''$literal'
    $credentials = @('--env', 'MONGO_USER=admin', '--env', "MONGO_USER_PASS=$password")

    # Exercise the actual importable egg's installer, including quoting of an
    # admin password containing spaces, an apostrophe, and a dollar sign.
    # An environment payload avoids PowerShell's added CRLF on piped text.
    $encodedScript = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($egg.scripts.installation.script))
    $installer = "$prefix-installer"
    $createdContainers += $installer
    $installerArguments = @('run', '--name', $installer, '--rm', '--network', 'none') + $credentials + @(
        '--mount', "type=volume,source=$volume,target=/mnt/server", '--env', "MANTA_INSTALL_SCRIPT_B64=$encodedScript",
        '--entrypoint', 'bash', $egg.scripts.installation.container, '-o', 'pipefail', '-c', 'printf %s "$MANTA_INSTALL_SCRIPT_B64" | base64 -d | bash')
    Invoke-MongoDocker $installerArguments | Out-Null
    Invoke-MongoDocker @('run', '--rm', '--network', 'none', '--mount', "type=volume,source=$volume,target=/mnt/server", '--entrypoint', 'chown', 'mongo:8.3.17', '-R', '988:988', '/mnt/server') | Out-Null

    $runtime = @('--detach', '--interactive', '--tty', '--network', 'none', '--read-only',
        '--user', '988:988', '--security-opt', 'no-new-privileges', '--cap-drop', 'ALL',
        '--memory', '2g', '--pids-limit', '512', '--tmpfs', '/tmp:rw,exec,nosuid,size=128m',
        '--mount', "type=volume,source=$volume,target=/home/container", '--env', 'SERVER_PORT=2001')

    # Reproduce the reported mismatch without touching a real database.
    $old = "$prefix-old"
    $createdContainers += $old
    Invoke-MongoDocker (@('run', '--name', $old) + $runtime + @('--env', 'MONGODB_CONFIG_OVERRIDE_NOFORK=true', '--entrypoint', 'mongod', 'mongo:8.2.11', '--config', '/home/container/mongod.conf', '--port', '2001', '--bind_ip', '0.0.0.0', '--logpath', '/proc/self/fd/1', '--logappend')) | Out-Null
    $oldState = Wait-MongoContainerExit $old
    $oldLogs = (Invoke-MongoDocker @('logs', $old)) -join "`n"
    if ($oldState.ExitCode -ne 62 -or $oldLogs -notmatch 'Invalid feature compatibility version') {
        throw "The expected 8.3/8.2 incompatibility was not reproduced (exit $($oldState.ExitCode))."
    }
    Write-Output 'PASS: reproduced the original FCV mismatch and exit 62.'

    $server = "$prefix-fixed"
    $createdContainers += $server
    Invoke-MongoDocker (@('run', '--name', $server) + $runtime + $credentials + @('--env', "STARTUP=$($egg.startup)", $image)) | Out-Null
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        $ErrorActionPreference = 'Continue'
        & docker exec $server mongosh --quiet --norc --host 127.0.0.1 --port 2001 --eval 'quit(db.adminCommand({ping: 1}).ok === 1 ? 0 : 1)' 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 500
    }
    $ErrorActionPreference = 'Stop'
    if (!$ready) { throw 'The corrected runtime did not become ready on port 2001.' }
    $write = "db.getSiblingDB('admin').auth(process.env.MONGO_USER, process.env.MONGO_USER_PASS); db.getSiblingDB('smoke').sentinel.insertOne({_id: 'preserved'});"
    Invoke-MongoDocker @('exec', $server, 'mongosh', '--quiet', '--norc', '--host', '127.0.0.1', '--port', '2001', '--eval', $write) | Out-Null
    Write-Output 'PASS: matched runtime accepts authenticated writes on port 2001.'

    $unauthenticatedRead = "db.getSiblingDB('smoke').sentinel.findOne();"
    $denied = Invoke-MongoDocker -DockerArguments @('exec', $server, 'mongosh', '--quiet', '--norc', '--host', '127.0.0.1', '--port', '2001', '--eval', $unauthenticatedRead) -ExpectedExitCodes @(1)
    if (($denied -join "`n") -notmatch 'requires authentication|not authorized|Unauthorized') {
        throw 'An unauthenticated read was not rejected as expected.'
    }
    Write-Output 'PASS: unauthenticated database reads are rejected.'

    Stop-MongoContainerGracefully $server
    $refused = Invoke-MongoDocker -DockerArguments $installerArguments -ExpectedExitCodes @(1)
    if (($refused -join "`n") -notmatch 'Existing MongoDB data found') { throw 'Reinstall did not refuse the existing data directory.' }
    Write-Output 'PASS: reinstall refuses to overwrite existing database files.'
    Invoke-MongoDocker @('start', $server) | Out-Null
    Start-Sleep -Seconds 2
    $read = "db.getSiblingDB('admin').auth(process.env.MONGO_USER, process.env.MONGO_USER_PASS); if (!db.getSiblingDB('smoke').sentinel.findOne({_id: 'preserved'})) { quit(1); }"
    Invoke-MongoDocker @('exec', $server, 'mongosh', '--quiet', '--norc', '--host', '127.0.0.1', '--port', '2001', '--eval', $read) | Out-Null
    Stop-MongoContainerGracefully $server
    Write-Output 'PASS: SIGTERM stops cleanly and data survives restart.'

    $broken = "$prefix-broken"
    $createdContainers += $broken
    Invoke-MongoDocker (@('run', '--name', $broken) + $runtime + @('--env', 'STARTUP=exec mongod --config /home/container/missing.conf', $image)) | Out-Null
    $brokenState = Wait-MongoContainerExit $broken
    $brokenLogs = (Invoke-MongoDocker @('logs', $broken)) -join "`n"
    if ($brokenState.ExitCode -eq 0 -or $brokenLogs -notmatch 'Error opening config file') {
        throw 'A failed startup did not expose its error and exit.'
    }
    Write-Output 'PASS: a failed startup exits with a visible error instead of waiting forever.'
} finally {
    $ErrorActionPreference = 'Continue'
    foreach ($name in $createdContainers) { & docker rm --force --volumes $name 2>&1 | Out-Null }
    if ($volumeCreated) { & docker volume rm $volume 2>&1 | Out-Null }
}
