#!/usr/bin/env pwsh
# Format check, vet, revive, and golangci-lint.
# PowerShell counterpart of the `verify` target in the Makefile.
$ErrorActionPreference = 'Stop'

$repoRoot = (git rev-parse --show-toplevel)
Set-Location $repoRoot
$toolsBin = Join-Path $repoRoot '.tools\bin'

# fmt-check: fail if any .go file is not gofmt-formatted.
$unformatted = (gofmt -l .)
if ($unformatted) {
    Write-Host 'These files need gofmt (run: gofmt -w .):'
    Write-Host $unformatted
    exit 1
}

go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$revive = Join-Path $toolsBin 'revive.exe'
$golangciLint = Join-Path $toolsBin 'golangci-lint.exe'
if (-not (Test-Path $revive) -or -not (Test-Path $golangciLint)) {
    & (Join-Path $PSScriptRoot 'lint-tools.ps1')
}

& $revive -config revive.toml ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $golangciLint run ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host 'verify passed'
