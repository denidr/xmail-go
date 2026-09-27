<#
.SYNOPSIS
  xmail release build script (PowerShell / native Windows version).

.DESCRIPTION
  Builds distributable artifacts for the 3 release targets. Functionally
  equivalent to scripts/release.sh — use
  this one if you don't have Git Bash/WSL; use release.sh from Git
  Bash/WSL/Linux/macOS/CI. Both write to dist/ and both are safe to
  re-run (each target only touches its own output).

  Targets:
    docker-amd64   linux/amd64 Docker image  (dist/*.tar, docker load-able)
    docker-arm64   linux/arm64 Docker image  (Armbian SBCs; dist/*.tar)
    windows-amd64  Windows x64 tray+service .exe
    all            all three, then dist/SHA256SUMS.txt

  Note: unlike release.sh (which gzips the Docker tarball), this script
  writes a plain, uncompressed .tar for the Docker targets to avoid
  depending on an external gzip binary on a bare Windows install. Both
  forms load fine with `docker load`.

.PARAMETER Target
  docker-amd64 | docker-arm64 | windows-amd64 | all

.PARAMETER Version
  Defaults to `git describe --tags --always --dirty`, or "dev" if that
  fails (no tags yet, or not a git repo).

.EXAMPLE
  scripts/release.ps1 -Target windows-amd64

.EXAMPLE
  scripts/release.ps1 -Target all -Version v0.1.0
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("docker-amd64", "docker-arm64", "windows-amd64", "all")]
    [string]$Target,

    [string]$Version
)

$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
Set-Location $RootDir

$DistDir = Join-Path $RootDir "dist"
$Image = if ($env:DOCKER_IMAGE) { $env:DOCKER_IMAGE } else { "xmail" }

if (-not $Version) {
    if ($env:VERSION) {
        $Version = $env:VERSION
    } else {
        try {
            $Version = (git describe --tags --always --dirty 2>$null)
            if (-not $Version) { $Version = "dev" }
        } catch {
            $Version = "dev"
        }
    }
}

function Write-Log($msg) { Write-Host "==> $msg" }

function Assert-Command($name) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        Write-Error "required command not found: $name"
        exit 1
    }
}

function Ensure-Dist {
    if (-not (Test-Path $DistDir)) { New-Item -ItemType Directory -Path $DistDir | Out-Null }
}

function Build-Docker([string]$Platform, [string]$ArchLabel) {
    Assert-Command docker
    Ensure-Dist

    $tag = "${Image}:${Version}-${ArchLabel}"
    $out = Join-Path $DistDir "xmail-$Version-linux-$ArchLabel-docker.tar"

    Write-Log "building docker $Platform -> $tag"
    docker buildx build --platform $Platform -t $tag --load $RootDir
    if ($LASTEXITCODE -ne 0) { throw "docker buildx build failed (exit $LASTEXITCODE)" }

    Write-Log "saving $tag -> $out"
    docker save -o $out $tag
    if ($LASTEXITCODE -ne 0) { throw "docker save failed (exit $LASTEXITCODE)" }

    $sizeMB = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Log "done: $out (${sizeMB}MB)"
}

function Build-DockerAmd64 { Build-Docker -Platform "linux/amd64" -ArchLabel "amd64" }

# Armbian SBCs (Orange Pi, Rock Pi, etc.) are overwhelmingly arm64
# these days; for an older 32-bit board, build linux/arm/v7 manually:
#   docker buildx build --platform linux/arm/v7 ...
function Build-DockerArm64 { Build-Docker -Platform "linux/arm64" -ArchLabel "arm64" }

function Build-WindowsAmd64 {
    Assert-Command go
    Ensure-Dist

    $out = Join-Path $DistDir "xmail-tray-windows-amd64-$Version.exe"
    Write-Log "building windows/amd64 (tray + service) -> $out"

    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    try {
        go build -tags xmailtray `
            -ldflags "-s -w -H=windowsgui -X main.version=$Version" `
            -o $out (Join-Path $RootDir "cmd\xmail-tray")
        if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
    } finally {
        Remove-Item Env:\GOOS, Env:\GOARCH, Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    }

    $sizeMB = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Log "done: $out (${sizeMB}MB)"
}

function Write-Checksums {
    Ensure-Dist
    $sumsFile = Join-Path $DistDir "SHA256SUMS.txt"
    Remove-Item $sumsFile -ErrorAction SilentlyContinue

    $lines = Get-ChildItem $DistDir -File | Where-Object { $_.Name -ne "SHA256SUMS.txt" } | ForEach-Object {
        $hash = (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLower()
        "$hash *$($_.Name)"
    }
    $lines | Set-Content -Path $sumsFile -Encoding ASCII
    Write-Log "wrote $sumsFile"
}

switch ($Target) {
    "docker-amd64"  { Build-DockerAmd64; Write-Checksums }
    "docker-arm64"  { Build-DockerArm64; Write-Checksums }
    "windows-amd64" { Build-WindowsAmd64; Write-Checksums }
    "all"           { Build-DockerAmd64; Build-DockerArm64; Build-WindowsAmd64; Write-Checksums }
}

Write-Log "release build complete (version=$Version)"
