$ErrorActionPreference = "Stop"

if ($env:EVAL_FINAL_MESSAGE -ne "windows-custom-engine-handled") {
    Write-Error "unexpected final message: $($env:EVAL_FINAL_MESSAGE)"
    exit 1
}
if (-not (Test-Path -LiteralPath "setup-marker.txt" -PathType Leaf)) {
    Write-Error "setup marker is missing"
    exit 1
}
if ((Get-Content -LiteralPath "result.txt" -Raw) -ne "windows-guest-complete") {
    Write-Error "result artifact is missing or invalid"
    exit 1
}

Write-Output "Windows setup, Custom Engine, and artifact verified"
