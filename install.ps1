#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = `
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo = 'alibaba/skill-up'
$Project = 'skill-up'
$RequestedVersion = if ($env:SKILL_UP_VERSION) { $env:SKILL_UP_VERSION } else { 'latest' }
$InstallDir = if ($env:INSTALL_DIR) {
    $env:INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\skill-up'
}

function Get-Arch {
    $a = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch -Regex ($a) {
        '^(AMD64|x86_64)$' { return 'amd64' }
        '^(ARM64|aarch64)$' { return 'arm64' }
    }
    throw "unsupported architecture: $a"
}

function Get-LatestTag {
    $url = "https://github.com/$Repo/releases/latest"
    $req = [System.Net.HttpWebRequest]::Create($url)
    $req.AllowAutoRedirect = $false
    $req.Method = 'HEAD'
    try {
        $resp = $req.GetResponse()
    } catch [System.Net.WebException] {
        $resp = $_.Exception.Response
        if (-not $resp) { throw }
    }
    try {
        $location = $resp.Headers['Location']
    } finally {
        $resp.Close()
    }
    if (-not $location) { throw "unable to resolve latest tag from $url" }
    return ($location -split '/tag/')[-1]
}

function Test-Checksum {
    param(
        [Parameter(Mandatory)] [string] $File,
        [Parameter(Mandatory)] [string] $ChecksumsFile
    )
    $name = [System.IO.Path]::GetFileName($File)
    $expected = $null
    foreach ($line in Get-Content -LiteralPath $ChecksumsFile) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $name) {
            $expected = $parts[0].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) {
        throw "no checksum entry for $name in $ChecksumsFile"
    }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $File).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "checksum mismatch for ${name} (expected $expected, got $actual)"
    }
}

$arch = Get-Arch
$os = 'windows'

$tag = $RequestedVersion
if ($tag -eq 'latest') {
    $tag = Get-LatestTag
} elseif (-not $tag.StartsWith('v')) {
    $tag = "v$tag"
}
$version = $tag.TrimStart('v')

$archive = "${Project}_${version}_${os}_${arch}.zip"
$baseUrl = "https://github.com/$Repo/releases/download/$tag"
$archiveUrl = "$baseUrl/$archive"
$checksumsUrl = "$baseUrl/${Project}_${version}_checksums.txt"

$tmpDir = New-Item -ItemType Directory -Force -Path `
    (Join-Path $env:TEMP "skill-up-install-$([guid]::NewGuid().ToString('N'))")

try {
    $archivePath = Join-Path $tmpDir $archive
    $checksumsPath = Join-Path $tmpDir 'checksums.txt'

    Write-Host "Downloading $archiveUrl"
    Invoke-WebRequest -Uri $archiveUrl -OutFile $archivePath -UseBasicParsing

    Write-Host 'Downloading checksums'
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath -UseBasicParsing
    Test-Checksum -File $archivePath -ChecksumsFile $checksumsPath

    Expand-Archive -LiteralPath $archivePath -DestinationPath $tmpDir -Force

    if (-not (Test-Path -LiteralPath $InstallDir)) {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    }

    $binaryName = "$Project.exe"
    $src = Join-Path $tmpDir $binaryName
    $dest = Join-Path $InstallDir $binaryName
    Copy-Item -LiteralPath $src -Destination $dest -Force

    Write-Host "Installed $binaryName to $InstallDir"
} finally {
    Remove-Item -LiteralPath $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}

$pathEntries = ($env:Path -split ';') | Where-Object { $_ }
if ($pathEntries -notcontains $InstallDir) {
    Write-Host ''
    Write-Host "$InstallDir is not in PATH. Add it for the current session with:"
    Write-Host ''
    Write-Host "  `$env:Path = `"$InstallDir;`$env:Path`""
    Write-Host ''
    Write-Host 'Or persist it for your user account with:'
    Write-Host ''
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$InstallDir;`" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')"
}
