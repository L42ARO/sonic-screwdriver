# Dot-sourced by deploy.ps1 and connect.ps1. Reads KEY=value pairs from .env.secrets.
function Get-DeploySetting([string]$Name) {
    $file = Join-Path $PSScriptRoot '.env.secrets'
    if (-not (Test-Path -LiteralPath $file)) { return $null }
    foreach ($line in Get-Content -LiteralPath $file) {
        if ($line -match '^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=\s*(.*?)\s*$' -and $Matches[1] -eq $Name) {
            $value = $Matches[2] -replace '^["'']|["'']$', ''
            if ($value) { return $value }
        }
    }
    return $null
}

function Get-RequiredDeploySetting([string]$Name, [string]$Parameter) {
    $value = Get-DeploySetting $Name
    if (-not $value) { throw "Set $Name in .env.secrets at the repository root (see .env.secrets.example), or pass -$Parameter." }
    return $value
}
