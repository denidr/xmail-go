<#
.SYNOPSIS
  xmail dev verification script (PowerShell / native Windows).

.DESCRIPTION
  Runs every check a contributor should pass before committing or
  pushing. Functionally equivalent to scripts/test.sh — use this one if
  you don't have Git Bash/WSL. Writes nothing to the repo (the optional
  -Coverage step writes the gitignored coverage.out). Safe to re-run.

  Steps, in order (fail-fast):
    1.  gofmt -l                      formatting drift
    2.  go mod tidy -diff             go.mod/go.sum drift
    3.  go vet ./...                  static analysis (default build)
    4.  go build ./...                default build compiles
    5.  tray+service compile check    -tags xmailtray (GOOS=windows)
    6.  go test ./...                 unit tests
    7.  go test -tags integration     in-process fake SMTP/IMAP/POP3
    8.  go test -tags xmailtray       tray + service tests (Windows host only)
    9.  node --check                  dashboard JS syntax (if node present)
    10. go test -race (opt-in)        -Race, needs cgo/gcc
    11. coverage report               -Coverage

.PARAMETER Race
  Also run the race detector. Requires cgo/gcc — not available on every
  dev machine; run it in CI before a release build.

.PARAMETER NoJs
  Skip the dashboard app.js syntax check.

.PARAMETER Coverage
  Also print a per-function coverage report.

.EXAMPLE
  scripts/test.ps1

.EXAMPLE
  scripts/test.ps1 -Race -Coverage
#>
[CmdletBinding()]
param(
    [switch]$Race,
    [switch]$NoJs,
    [switch]$Coverage
)

$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
Set-Location $RootDir

function Step($msg) { Write-Host ""; Write-Host "==> $msg" }
function Ok { Write-Host "    ok" }
function Skip($why) { Write-Host "    skipped: $why" }

function Fail($msg) {
    Write-Host "error: $msg" -ForegroundColor Red
    exit 1
}

function Assert-Command($name) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) { Fail "required command not found: $name" }
}

function Invoke-Checked([string]$Desc, [scriptblock]$Action) {
    Step $Desc
    & $Action
    if ($LASTEXITCODE -ne 0) { Fail "$Desc failed (exit $LASTEXITCODE)" }
    Ok
}

# Restores an env var to its prior value, removing it if it was unset.
function Restore-Env([string]$Name, $Value) {
    if ($null -eq $Value) { Remove-Item "Env:\$Name" -ErrorAction SilentlyContinue }
    else { Set-Item "Env:\$Name" $Value }
}

Assert-Command go

Step "gofmt -l cmd internal (formatting drift)"
$unformatted = & gofmt -l cmd internal
if ($LASTEXITCODE -ne 0) { Fail "gofmt failed to run (exit $LASTEXITCODE)" }
if ($unformatted) {
    Write-Host "error: not gofmt-clean, run: gofmt -w cmd internal" -ForegroundColor Red
    $unformatted | ForEach-Object { Write-Host "  $_" }
    exit 1
}
Ok

Step "go mod tidy -diff (go.mod/go.sum drift)"
$tidyDiff = & go mod tidy -diff 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "error: go.mod/go.sum are not tidy, run: go mod tidy" -ForegroundColor Red
    $tidyDiff | ForEach-Object { Write-Host "  $_" }
    exit 1
}
Ok

Invoke-Checked "go vet ./..." { go vet ./... }

Invoke-Checked "go build ./..." { go build ./... }

# The tray+service files are gated `//go:build windows && xmailtray`; on a
# non-Windows host a plain `-tags xmailtray` build fails with "build
# constraints exclude all Go files". Setting GOOS=windows makes the check
# work everywhere (mirrors scripts/test.sh).
Step "tray+service compile check (GOOS=windows -tags xmailtray)"
$prevGOOS = $env:GOOS
$prevCGO = $env:CGO_ENABLED
try {
    $env:GOOS = "windows"
    $env:CGO_ENABLED = "0"
    go build -tags xmailtray ./...
    if ($LASTEXITCODE -ne 0) { Fail "go build -tags xmailtray failed" }
    go vet -tags xmailtray ./...
    if ($LASTEXITCODE -ne 0) { Fail "go vet -tags xmailtray failed" }
} finally {
    Restore-Env "GOOS" $prevGOOS
    Restore-Env "CGO_ENABLED" $prevCGO
}
Ok

Invoke-Checked "go test ./... (unit)" { go test ./... }

Invoke-Checked "go test -tags integration ./... (in-process fake SMTP/IMAP/POP3)" { go test -tags integration ./... }

Step "go test -tags xmailtray (Windows host only)"
if ((go env GOOS) -eq "windows") {
    go test -tags xmailtray ./internal/winservice/ ./cmd/xmail-tray/
    if ($LASTEXITCODE -ne 0) { Fail "xmailtray tests failed (exit $LASTEXITCODE)" }
    Ok
} else {
    Skip "host GOOS=$(go env GOOS) — the winservice/xmailtray build tags require windows"
}

Step "dashboard JS syntax (node --check)"
if (-not $NoJs) {
    if (Get-Command node -ErrorAction SilentlyContinue) {
        node --check internal/dashboard/assets/app.js
        if ($LASTEXITCODE -ne 0) { Fail "app.js syntax check failed" }
        Ok
    } else {
        Skip "node not found"
    }
} else {
    Skip "-NoJs"
}

Step "go test -race -tags integration ./... (race detector)"
if ($Race) {
    go test -race -tags integration ./...
    if ($LASTEXITCODE -ne 0) { Fail "race detector run failed (exit $LASTEXITCODE)" }
    Ok
} else {
    Skip "needs cgo/gcc — re-run with -Race"
}

if ($Coverage) {
    Step "coverage (go test -coverprofile / go tool cover)"
    go test -coverprofile=coverage.out ./...
    if ($LASTEXITCODE -ne 0) { Fail "coverage run failed (exit $LASTEXITCODE)" }
    go tool cover -func=coverage.out
    if ($LASTEXITCODE -ne 0) { Fail "go tool cover failed (exit $LASTEXITCODE)" }
    Ok
}

Write-Host ""
Write-Host "==> all checks passed"
