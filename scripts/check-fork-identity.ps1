[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent
$expectedModule = 'module github.com/omriariav/amq-squad/v2'
$expectedRepository = 'https://github.com/focofacofoco/amq-squad'

function Assert-Equal {
    param([object]$Actual, [object]$Expected, [string]$Label)
    if ($Actual -ne $Expected) {
        throw "$Label mismatch: expected '$Expected', got '$Actual'"
    }
}

$moduleLine = Get-Content -LiteralPath (Join-Path $repoRoot 'go.mod') -First 1
Assert-Equal $moduleLine $expectedModule 'Go module'

$codexManifest = Get-Content -LiteralPath (Join-Path $repoRoot 'plugins\codex\.codex-plugin\plugin.json') -Raw | ConvertFrom-Json
$claudeManifest = Get-Content -LiteralPath (Join-Path $repoRoot 'plugins\claude\.claude-plugin\plugin.json') -Raw | ConvertFrom-Json
$claudeMarketplace = Get-Content -LiteralPath (Join-Path $repoRoot '.claude-plugin\marketplace.json') -Raw | ConvertFrom-Json

Assert-Equal $codexManifest.name 'amq-squad' 'Codex plugin name'
Assert-Equal $claudeManifest.name 'amq-squad' 'Claude plugin name'
Assert-Equal $claudeManifest.repository $expectedRepository 'Claude plugin repository'
Assert-Equal $claudeManifest.homepage $expectedRepository 'Claude plugin homepage'
Assert-Equal $claudeMarketplace.owner.url 'https://github.com/focofacofoco' 'Claude marketplace owner'

Write-Output 'fork identity: PASS'
