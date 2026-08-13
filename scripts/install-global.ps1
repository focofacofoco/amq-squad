[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$Check,
    [string]$Commit = '59d6cc11bb7b2aaa43579218425301356c71a8e5'
)

$ErrorActionPreference = 'Stop'

$RepositoryRoot = Split-Path $PSScriptRoot -Parent
$InstallRoot = Join-Path $env:LOCALAPPDATA "amq-squad\$Commit"
$CodexMarketplace = $InstallRoot
$ClaudeMarketplace = $InstallRoot
$BinaryPath = Join-Path $HOME '.local\bin\amq-squad.exe'

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)

    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Program failed with exit code $LASTEXITCODE"
    }
}

if ($Check) {
    $version = & amq-squad version --json | ConvertFrom-Json
    if ($version.data.fork_owner -ne 'focofacofoco' -or $version.data.fork_commit -ne $Commit) {
        throw "amq-squad on PATH is not the expected Facode fork commit $Commit"
    }
    Invoke-Checked codex @('plugin', 'list')
    Invoke-Checked claude @('plugin', 'list')
    Write-Host 'Facode AMQ Squad global integration: PASS'
    exit 0
}

if ($Uninstall) {
    & codex plugin remove amq-squad@facode-amq-squad
    & codex plugin marketplace remove facode-amq-squad
    & claude plugin uninstall amq-squad
    & claude plugin marketplace remove amq-squad
    exit 0
}

if (-not (Test-Path -LiteralPath (Join-Path $InstallRoot 'FORK.md'))) {
    $archive = Join-Path $env:TEMP "amq-squad-$Commit.tar.gz"
    $staging = Join-Path $env:TEMP "amq-squad-$Commit"
    Invoke-WebRequest -Uri "https://codeload.github.com/focofacofoco/amq-squad/tar.gz/$Commit" -OutFile $archive
    if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging -Recurse -Force }
    New-Item -ItemType Directory -Path $staging | Out-Null
    & tar -xzf $archive -C $staging --strip-components=1
    if ($LASTEXITCODE -ne 0) { throw 'extract fork archive failed' }
    New-Item -ItemType Directory -Path (Split-Path $InstallRoot -Parent) -Force | Out-Null
    Move-Item -LiteralPath $staging -Destination $InstallRoot
}

New-Item -ItemType Directory -Path (Split-Path $BinaryPath -Parent) -Force | Out-Null
Push-Location $InstallRoot
try {
    $linkFlags = "-X github.com/omriariav/amq-squad/v2/internal/forkinfo.Commit=$Commit -X github.com/omriariav/amq-squad/v2/internal/forkinfo.Modified=true"
    Invoke-Checked go @('build', '-ldflags', $linkFlags, '-o', $BinaryPath, './cmd/amq-squad')
} finally {
    Pop-Location
}

& codex plugin remove amq-squad@facode-amq-squad
& codex plugin marketplace remove facode-amq-squad
& claude plugin uninstall amq-squad@amq-squad
& claude plugin marketplace remove amq-squad

Invoke-Checked codex @('plugin', 'marketplace', 'add', $CodexMarketplace)
Invoke-Checked codex @('plugin', 'add', 'amq-squad@facode-amq-squad', '--json')
Invoke-Checked claude @('plugin', 'marketplace', 'add', $ClaudeMarketplace)
Invoke-Checked claude @('plugin', 'install', 'amq-squad@amq-squad')

Write-Host 'Installed Facode AMQ Squad globally for Codex and Claude.'
