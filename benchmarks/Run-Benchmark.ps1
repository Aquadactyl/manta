param(
    [ValidateSet('lifecycle','performance','resources','security','events')]
    [string[]]$Phases = @('lifecycle','performance','resources','security'),
    [string]$PanelContainer = 'aquadactyl-test-panel-1',
    [string]$OutputDirectory = (Join-Path $PSScriptRoot 'results')
)
$ErrorActionPreference='Stop'
$workspacePath=Split-Path -Parent $PSScriptRoot
$resultsPath=[IO.Path]::GetFullPath($OutputDirectory)
$privatePath=Join-Path ([IO.Path]::GetTempPath()) ('manta-benchmark-'+[guid]::NewGuid().ToString('N')+'.json')
$containerId=$null
$probePids=@{}

function Invoke-BenchmarkDocker {
    & docker @args
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $LASTEXITCODE" }
}
function Stop-LoopbackProbes {
    foreach ($entry in $probePids.GetEnumerator()) {
        # Only stop a listener started by this invocation, after checking its command line.
        $stopProbe='if /usr/local/bin/busybox grep -q /tmp/manta-benchmark-probe "/proc/$1/cmdline" 2>/dev/null; then /usr/local/bin/busybox kill "$1"; fi'
        & docker exec $entry.Key /usr/local/bin/busybox sh -c $stopProbe sh $entry.Value 2>$null | Out-Null
        & docker exec $entry.Key /usr/local/bin/busybox rm -f /tmp/manta-benchmark-probe/pid 2>$null | Out-Null
    }
    $probePids.Clear()
}
try {
    Invoke-BenchmarkDocker cp (Join-Path $PSScriptRoot 'provision.php') "${PanelContainer}:/tmp/manta-benchmark-provision.php"
    $raw=Invoke-BenchmarkDocker exec -w /app $PanelContainer php /tmp/manta-benchmark-provision.php manifest
    [IO.File]::WriteAllText($privatePath,($raw -join "`n"),[Text.UTF8Encoding]::new($false))
    $raw=$null
    New-Item -ItemType Directory -Path $resultsPath -Force | Out-Null
    Invoke-BenchmarkDocker run --rm --mount "type=bind,source=$workspacePath,target=/workspace" `
        --mount 'type=volume,source=manta-go-mod,target=/go/pkg/mod' `
        --mount 'type=volume,source=manta-go-build,target=/root/.cache/go-build' `
        -w /workspace golang:1.26.9-bookworm go build -o /workspace/build/manta-benchmark ./benchmarks/runner
    foreach ($phase in $Phases) {
        if ($phase -eq 'security') {
            $probe=@'
/usr/local/bin/busybox mkdir -p /tmp/manta-benchmark-probe
printf 'controlled-loopback-benchmark\n' > /tmp/manta-benchmark-probe/sentinel.txt
/usr/local/bin/busybox httpd -f -p 127.0.0.1:19090 -h /tmp/manta-benchmark-probe &
printf '%s' "$!" > /tmp/manta-benchmark-probe/pid
'@
            foreach ($daemon in @('manta-test','wings-benchmark')) {
                Invoke-BenchmarkDocker exec $daemon /usr/local/bin/busybox sh -c $probe
                $probePid=Invoke-BenchmarkDocker exec $daemon /usr/local/bin/busybox cat /tmp/manta-benchmark-probe/pid
                if ($probePid -notmatch '^\d+$') { throw 'Invalid loopback probe process ID' }
                $probePids[$daemon]=$probePid
                $sentinel=Invoke-BenchmarkDocker exec $daemon /usr/local/bin/busybox wget -q -O - http://127.0.0.1:19090/sentinel.txt
                if ($sentinel.Trim() -ne 'controlled-loopback-benchmark') { throw 'Loopback probe not ready' }
            }
        }
        $containerId=Invoke-BenchmarkDocker create --name "manta-benchmark-$phase" --cpus 4 --memory 1g `
            --network aquadactyl-test_default `
            --mount "type=bind,source=$workspacePath\build\manta-benchmark,target=/runner,readonly" `
            --mount "type=bind,source=$privatePath,target=/private/manifest.json,readonly" `
            --mount "type=bind,source=$resultsPath,target=/results" `
            --mount 'type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock' `
            --entrypoint /runner golang:1.26.9-bookworm -phase $phase
        if ($phase -eq 'resources') {
            Invoke-BenchmarkDocker network connect manta-test_servers $containerId
            Invoke-BenchmarkDocker network connect manta-benchmark-wings_servers $containerId
        }
        Invoke-BenchmarkDocker start -a $containerId
        $exitCode=Invoke-BenchmarkDocker inspect --format '{{.State.ExitCode}}' $containerId
        if ($exitCode -ne '0') { throw "Benchmark phase $phase failed with exit code $exitCode" }
        Invoke-BenchmarkDocker rm $containerId | Out-Null
        $containerId=$null
        if ($phase -eq 'security') { Stop-LoopbackProbes }
    }
    Write-Output "Benchmark artifacts saved in $resultsPath."
}
finally {
    if ($containerId -and $containerId -match '^[a-f0-9]{64}$') {
        & docker rm --force $containerId 2>$null | Out-Null
    }
    if ('security' -in $Phases) { Stop-LoopbackProbes }
    if (Test-Path -LiteralPath $privatePath) { Remove-Item -LiteralPath $privatePath -Force }
}
