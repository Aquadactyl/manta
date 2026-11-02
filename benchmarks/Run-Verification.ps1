param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build\verification'),
    [string]$Python = 'python',
    [ValidateRange(0, 100)][int]$MinimumCoverage = 100
)

$ErrorActionPreference = 'Stop'
$workspacePath = Split-Path -Parent $PSScriptRoot
$resultsPath = [IO.Path]::GetFullPath($OutputDirectory)
$prefix = 'manta-verification-' + [guid]::NewGuid().ToString('N')
$engineCreated = $false
$networkCreated = $false
$volumesCreated = @()
$coverageStatus = $null

function Invoke-VerificationDocker {
    & docker @args
    if ($LASTEXITCODE -ne 0) { throw "Verification Docker command failed ($LASTEXITCODE)." }
}

try {
    New-Item -ItemType Directory -Path $resultsPath -Force | Out-Null
    Invoke-VerificationDocker network create $prefix | Out-Null
    $networkCreated = $true
    foreach ($suffix in @('socket', 'tmp')) {
        $volume = "$prefix-$suffix"
        Invoke-VerificationDocker volume create $volume | Out-Null
        $volumesCreated += $volume
    }
    # An isolated engine owns all containers/networks created by the Go tests.
    # It publishes no host ports and shares only its socket and temporary files
    # with the test container, not the workstation's Docker socket.
    Invoke-VerificationDocker run --detach --name $prefix --privileged --network $prefix `
        --env DOCKER_TLS_CERTDIR= `
        --mount "type=volume,source=$prefix-socket,target=/var/run" `
        --mount "type=volume,source=$prefix-tmp,target=/tmp" `
        --health-cmd 'docker -H unix:///var/run/docker.sock info >/dev/null 2>&1' `
        --health-interval 1s --health-start-period 5s --health-retries 45 `
        docker:29-dind dockerd --host=unix:///var/run/docker.sock --tls=false | Out-Null
    $engineCreated = $true
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        $state = (Invoke-VerificationDocker inspect $prefix | ConvertFrom-Json)[0].State
        if (!$state.Running -or $state.Health.Status -eq 'unhealthy') { throw 'Isolated Docker engine failed its health check.' }
        if ($state.Health.Status -eq 'healthy') { $ready = $true; break }
        Start-Sleep -Seconds 1
    }
    if (!$ready) { throw 'Isolated Docker engine did not become ready.' }
    $engineVersion = Invoke-VerificationDocker exec $prefix docker version --format '{{.Server.Version}}'
    foreach ($image in @('ghcr.io/pterodactyl/installers:alpine', 'alpine:latest')) {
        Invoke-VerificationDocker exec $prefix docker pull $image | Out-Null
    }
    $testScript = @'
set -e
go version > /results/go-version.txt
go test -race -json -count=1 -coverpkg=./... -covermode=atomic -coverprofile=/results/coverage.out ./... > /results/race.json
go vet ./...
'@
    Invoke-VerificationDocker run --rm --cpus 4 --memory 4g --network $prefix `
        --mount "type=bind,source=$workspacePath,target=/workspace" `
        --mount "type=bind,source=$resultsPath,target=/results" `
        --mount "type=volume,source=$prefix-socket,target=/var/run" `
        --mount "type=volume,source=$prefix-tmp,target=/tmp" `
        --mount 'type=volume,source=manta-go-mod,target=/go/pkg/mod' `
        --mount 'type=volume,source=manta-go-build,target=/root/.cache/go-build' `
        -w /workspace golang:1.26.9-bookworm sh -c $testScript

    $rows = @(Get-Content -LiteralPath (Join-Path $resultsPath 'race.json') | ForEach-Object { $_ | ConvertFrom-Json })
    $failed = @($rows | Where-Object { $_.Action -eq 'fail' }).Count
    $skipped = @($rows | Where-Object { $_.Action -eq 'skip' }).Count
    if ($failed -or $skipped) { throw 'Verification requires zero failed or skipped cases.' }
    $validation = [ordered]@{
        date = (Get-Date -Format 'yyyy-MM-dd')
        race = 'passed'
        vet = 'passed'
        failed = $failed
        skipped = $skipped
        passedCases = @($rows | Where-Object { $_.Action -eq 'pass' -and $_.Test }).Count
        passedPackages = @($rows | Where-Object { $_.Action -eq 'pass' -and !$_.Test }).Count
        testToolchain = (Get-Content -LiteralPath (Join-Path $resultsPath 'go-version.txt') -Raw).Trim()
        command = 'go test -race -json -count=1 -coverpkg=./... -covermode=atomic -coverprofile=coverage.out ./...'
        coverageProfileSha256 = (Get-FileHash -LiteralPath (Join-Path $resultsPath 'coverage.out') -Algorithm SHA256).Hash.ToLowerInvariant()
        coverageLogSha256 = (Get-FileHash -LiteralPath (Join-Path $resultsPath 'race.json') -Algorithm SHA256).Hash.ToLowerInvariant()
        testEngine = "Docker $engineVersion isolated Docker-in-Docker"
    }
    [IO.File]::WriteAllText((Join-Path $resultsPath 'validation.json'), ($validation | ConvertTo-Json -Depth 10) + "`n", [Text.UTF8Encoding]::new($false))
    & $Python (Join-Path $PSScriptRoot 'coverage_report.py') (Join-Path $resultsPath 'coverage.out') `
        --output (Join-Path $resultsPath 'coverage.json') --minimum $MinimumCoverage
    $coverageStatus = $LASTEXITCODE
    if ($coverageStatus -notin @(0, 1)) { throw 'Coverage profile could not be audited.' }
    Write-Output "$($validation.passedCases) Go cases/subtests in $($validation.passedPackages) packages passed with race detection; vet passed."
} finally {
    # Remove only resources successfully created by this invocation.
    if ($engineCreated) { & docker rm --force --volumes $prefix | Out-Null }
    foreach ($volume in $volumesCreated) { & docker volume rm $volume | Out-Null }
    if ($networkCreated) { & docker network rm $prefix | Out-Null }
}

if ($coverageStatus -ne 0) { throw "The $MinimumCoverage% statement-coverage target is not met. Exact gaps are in $resultsPath\coverage.json." }
