param([Parameter(Mandatory = $true)][string]$InputFile)

$ErrorActionPreference = "Stop"
if (-not (Test-Path -LiteralPath $InputFile -PathType Leaf)) {
    throw "SessionInput file is missing: $InputFile"
}
if (-not (Test-Path -LiteralPath "setup-marker.txt" -PathType Leaf)) {
    throw "setup step marker is missing"
}

Set-Content -LiteralPath "result.txt" -Value "windows-guest-complete" -NoNewline
@{
    exit_code = 0
    final_message = "windows-custom-engine-handled"
    turns = 1
    transcript = @(
        @{ role = "user"; content = "run" }
        @{ role = "assistant"; content = "windows-custom-engine-handled" }
    )
} | ConvertTo-Json -Depth 4 -Compress
