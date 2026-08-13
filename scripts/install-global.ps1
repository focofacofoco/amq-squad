[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$Check,
    [string]$Commit = 'afac6fb9a47b423dee9729c5203c01098afc47c3'
)

$ErrorActionPreference = 'Stop'

$RepositoryRoot = Split-Path $PSScriptRoot -Parent
$InstallRoot = Join-Path $env:LOCALAPPDATA "amq-squad\$Commit"
$CodexMarketplace = $InstallRoot
$ClaudeMarketplace = $InstallRoot

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)

    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Program failed with exit code $LASTEXITCODE"
    }
}

if ($Check) {
    $version = & amq-squad version --json | ConvertFrom-Json
    if ($version.data.fork_owner -ne 'focofacofoco') {
        throw 'amq-squad on PATH is not the Facode fork'
    }
    Invoke-Checked codex @('plugin', 'list')
    Invoke-Checked claude @('plugin', 'list')
    Write-Host 'Facode AMQ Squad global integration: PASS'
    exit 0
}

if ($Uninstall) {
    & codex plugin remove amq-squad
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

Invoke-Checked codex @('plugin', 'marketplace', 'add', $CodexMarketplace)
Invoke-Checked codex @('plugin', 'add', 'amq-squad@facode-amq-squad', '--json')
Invoke-Checked claude @('plugin', 'marketplace', 'add', $ClaudeMarketplace)
Invoke-Checked claude @('plugin', 'install', 'amq-squad@amq-squad')

Write-Host 'Installed Facode AMQ Squad globally for Codex and Claude.'
