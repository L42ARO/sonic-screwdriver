[CmdletBinding()]
param(
    [ValidatePattern('^[a-zA-Z0-9.-]+$')][string]$HostName,
    [ValidatePattern('^[a-z_][a-z0-9_-]*$')][string]$UserName,
    [string]$KeyPath,
    [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9_-]*$')][string]$RemoteDirectory = 'sonic-sd-compute-box',
    [ValidateRange(1024,65535)][int]$Port = 6080,
    [string]$EnvironmentFile = (Join-Path $PSScriptRoot 'compute-box\.env'),
    [switch]$UpdateEnvironment
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'deploy-settings.ps1')
# Assigned via a temporary so ValidatePattern also checks values from .env.secrets.
if (-not $HostName) { $configured = Get-RequiredDeploySetting 'SONIC_DEPLOY_HOST' 'HostName'; $HostName = $configured }
if (-not $UserName) { $configured = Get-RequiredDeploySetting 'SONIC_DEPLOY_USER' 'UserName'; $UserName = $configured }
if (-not $KeyPath) { $KeyPath = Join-Path $PSScriptRoot (Get-RequiredDeploySetting 'SONIC_DEPLOY_KEY' 'KeyPath') }
foreach ($program in @('ssh', 'scp', 'tar')) { Get-Command $program -ErrorAction Stop | Out-Null }
$resolvedKey = (Resolve-Path -LiteralPath $KeyPath).Path
$destination = "$UserName@$HostName"
$sshOptions = @('-i', $resolvedKey, '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', '-o', 'StrictHostKeyChecking=accept-new')

function Invoke-RemoteScript([string]$Script) {
    # Base64 transports shell syntax intact through Windows PowerShell/native quoting.
    # The script itself never contains credentials; .env travels separately over SCP.
    $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Script.Replace("`r`n", "`n")))
    & ssh @sshOptions $destination "echo $encoded | base64 -d | bash"
    if ($LASTEXITCODE -ne 0) { throw "Remote command failed (exit $LASTEXITCODE)." }
}

$sourceDirectory = Join-Path $PSScriptRoot 'compute-box'
$taskDirectory = Join-Path ([IO.Path]::GetTempPath()) ('sonic-deploy-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskDirectory | Out-Null
$archivePath = Join-Path $taskDirectory 'source.tar.gz'
$filteredEnvironment = Join-Path $taskDirectory 'runtime.env'

try {
    Write-Host "Checking Docker and resources on $destination..."
    Invoke-RemoteScript @"
set -eu
command -v docker >/dev/null
docker compose version
available_kb=`$(df -Pk / | awk 'NR==2 {print `$4}')
if [ "`$available_kb" -lt 1048576 ]; then
  echo 'Less than 1 GiB free on the root disk. Free space before deploying; no unrelated Docker resources will be removed.' >&2
  exit 1
fi
umask 077
mkdir -p "`$HOME/$RemoteDirectory"
chmod 700 "`$HOME/$RemoteDirectory"
"@

    # Explicit source list: no local databases, screenshots, private keys or .env in tar.
    & tar -czf $archivePath -C $sourceDirectory Dockerfile compose.yaml .dockerignore go.mod go.sum main.go src scripts
    if ($LASTEXITCODE -ne 0) { throw 'Creating the source archive failed.' }
    & scp @sshOptions $archivePath "${destination}:${RemoteDirectory}/source.tar.gz"
    if ($LASTEXITCODE -ne 0) { throw 'Uploading source failed.' }

    if (Test-Path -LiteralPath $EnvironmentFile) {
        $allowedNames = @('OPENAI_KEY', 'OPENAI_API_KEY', 'SONIC_MODEL', 'SONIC_REASONING', 'SONIC_BRAIN', 'SONIC_BUDGET_USD', 'SONIC_NTFY_TOPIC', 'SONIC_JOB_TIMEOUT', 'SONIC_MAX_STEPS')
        $lines = foreach ($line in Get-Content -LiteralPath $EnvironmentFile) {
            if ($line -match '^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=(.*)$' -and $allowedNames -contains $Matches[1]) {
                '{0}={1}' -f $Matches[1], $Matches[2]
            }
        }
        if (-not $lines) { throw 'The local environment file contains no supported compute-box settings.' }
        [IO.File]::WriteAllText($filteredEnvironment, (($lines -join "`n") + "`n"), (New-Object Text.UTF8Encoding($false)))
        & scp @sshOptions $filteredEnvironment "${destination}:${RemoteDirectory}/.env.upload"
        if ($LASTEXITCODE -ne 0) { throw 'Uploading runtime settings failed.' }
    }

    $replaceEnvironment = if ($UpdateEnvironment) { '1' } else { '0' }
    Write-Host 'Building on EC2 and waiting for the container health check...'
    Invoke-RemoteScript @"
set -eu
cd "`$HOME/$RemoteDirectory"
umask 077
if [ -f .env.upload ]; then
  if [ ! -f .env ] || [ '$replaceEnvironment' = 1 ]; then
    mv .env.upload .env
  else
    rm .env.upload
    echo 'Preserving existing server .env (use -UpdateEnvironment to replace it).'
  fi
fi
if [ ! -f .env ]; then
  echo 'No server .env exists. Supply -EnvironmentFile with an OpenAI key or scripted-brain settings.' >&2
  exit 1
fi
chmod 600 .env
# Replace the uploaded source exactly, so files deleted locally don't linger and break the build.
# Only these archive paths are removed; .env, .deployment.env and the data volume are untouched.
rm -rf Dockerfile compose.yaml .dockerignore go.mod go.sum main.go src scripts
tar -xzf source.tar.gz
rm source.tar.gz
printf 'SONIC_PORT=$Port\nSONIC_PUBLISH_ADDRESS=0.0.0.0\n' > .deployment.env
docker compose --env-file .deployment.env config --quiet
DOCKER_BUILDKIT=1 COMPOSE_BAKE=false docker compose --env-file .deployment.env up --build --detach --wait --wait-timeout 120
docker compose --env-file .deployment.env ps
curl --fail --silent --show-error http://127.0.0.1:$Port/healthz
echo
"@
    Write-Host "Deployed to /home/$UserName/$RemoteDirectory. SQLite, screenshots and the agent token persist in the Docker data volume."
    Write-Host "Local agent and SDK server URL: http://${HostName}:$Port"
    Write-Host 'To copy the cloud agent token: .\connect.ps1 -AgentToken'
}
finally {
    # Only remove the exact temporary files created above; never recurse into user paths.
    foreach ($file in @($archivePath, $filteredEnvironment)) {
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
    }
    if (Test-Path -LiteralPath $taskDirectory) { Remove-Item -LiteralPath $taskDirectory }
}
