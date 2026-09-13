$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Force 'build/windows' | Out-Null
    & go build -buildvcs=false -trimpath -tags 'desktop,production' -ldflags '-s -w -H windowsgui' -o 'build/windows/DengShell.exe' .
    if ($LASTEXITCODE -ne 0) { throw 'DengShell Windows build failed.' }
    Write-Host 'Built: build/windows/DengShell.exe'
} finally {
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
    $env:CGO_ENABLED = $previousCgo
}
