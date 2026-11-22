[CmdletBinding()]
param(
    [switch]$Tunnel,
    [switch]$AgentToken,
    [ValidateRange(1024,65535)][int]$Port = 6080,
    [ValidatePattern('^[a-zA-Z0-9.-]+$')][string]$HostName,
    [ValidatePattern('^[a-z_][a-z0-9_-]*$')][string]$UserName,
    [string]$KeyPath
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'deploy-settings.ps1')
# Assigned via a temporary so ValidatePattern also checks values from .env.secrets.
if (-not $HostName) { $configured = Get-RequiredDeploySetting 'SONIC_DEPLOY_HOST' 'HostName'; $HostName = $configured }
if (-not $UserName) { $configured = Get-RequiredDeploySetting 'SONIC_DEPLOY_USER' 'UserName'; $UserName = $configured }
if (-not $KeyPath) { $KeyPath = Join-Path $PSScriptRoot (Get-RequiredDeploySetting 'SONIC_DEPLOY_KEY' 'KeyPath') }
$resolvedKey = (Resolve-Path -LiteralPath $KeyPath).Path
$options = @('-i', $resolvedKey, '-o', 'ServerAliveInterval=30', '-o', 'ServerAliveCountMax=3', '-o', 'StrictHostKeyChecking=accept-new')
if ($Tunnel -and $AgentToken) { throw 'Choose -Tunnel or -AgentToken, not both.' }
if ($Tunnel) {
    Write-Host "Forwarding http://localhost:$Port to the EC2 compute-box. Leave this terminal open."
    & ssh @options -o ExitOnForwardFailure=yes -N -L "127.0.0.1:${Port}:127.0.0.1:${Port}" "$UserName@$HostName"
} elseif ($AgentToken) {
    & ssh @options "$UserName@$HostName" 'cd sonic-sd-compute-box && docker compose exec -T compute-box cat /data/agent-token'
} else {
    & ssh @options -t "$UserName@$HostName" 'tmux new-session -A -s ssd'
}
if ($LASTEXITCODE -ne 0) { throw "SSH exited with code $LASTEXITCODE." }
