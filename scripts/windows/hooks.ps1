#!/usr/bin/env pwsh
# Point Git at this repo's hooks (run once per clone).
# PowerShell counterpart of the `hooks` target in the Makefile, for Windows
# contributors who do not have GNU make available.
$ErrorActionPreference = 'Stop'

Set-Location (git rev-parse --show-toplevel)
if ((git config core.hooksPath) -ne '.githooks') {
    git config core.hooksPath .githooks
    Write-Host 'git hooks installed (.githooks)'
}
