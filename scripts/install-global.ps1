[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

$RepositoryRoot = Split-Path $PSScriptRoot -Parent
$CodexMarketplace = $RepositoryRoot
$ClaudeMarketplace = $RepositoryRoot

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

Invoke-Checked codex @('plugin', 'marketplace', 'add', $CodexMarketplace)
Invoke-Checked codex @('plugin', 'add', 'amq-squad@facode-amq-squad', '--json')
Invoke-Checked claude @('plugin', 'marketplace', 'add', $ClaudeMarketplace)
Invoke-Checked claude @('plugin', 'install', 'amq-squad@amq-squad')

Write-Host 'Installed Facode AMQ Squad globally for Codex and Claude.'
