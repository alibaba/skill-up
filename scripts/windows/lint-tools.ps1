#!/usr/bin/env pwsh
# Install the pinned lint tools into .tools/bin.
# PowerShell counterpart of the `lint-tools` target in the Makefile. The tool
# versions are kept in sync with the Makefile (GOLANGCI_LINT_VERSION /
# REVIVE_VERSION).
$ErrorActionPreference = 'Stop'

$golangciLintVersion = 'v2.11.4'
$reviveVersion = 'v1.10.0'

$repoRoot = (git rev-parse --show-toplevel)
$toolsBin = Join-Path $repoRoot '.tools\bin'
New-Item -ItemType Directory -Force -Path $toolsBin | Out-Null

$env:GOBIN = $toolsBin
$env:GOFLAGS = '-buildvcs=false'

go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$golangciLintVersion"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go install "github.com/mgechev/revive@$reviveVersion"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "lint tools installed into $toolsBin"
