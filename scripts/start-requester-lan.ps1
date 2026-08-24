param(
    [Parameter(Mandatory = $true)]
    [string]$ControlURL,
    [string]$ClientSecret = 'lan-dev-client-change-me',
    [string]$ModelDir = "$env:LOCALAPPDATA\AIPool\models",
    [string]$Models = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$proxyBinary = Join-Path $PSScriptRoot 'proxy.exe'
if (-not (Test-Path -LiteralPath $proxyBinary)) { $proxyBinary = Join-Path $repoRoot 'bin\proxy.exe' }
$ControlURL = $ControlURL.TrimEnd('/')

$listener = Get-NetTCPConnection -State Listen -LocalPort 11434 -ErrorAction SilentlyContinue | Select-Object -First 1
if ($listener) { throw "TCP port 11434 is already used by PID $($listener.OwningProcess); stop the existing service before starting AIPool Proxy." }

Write-Host "Checking control plane at $ControlURL ..."
Invoke-RestMethod "$ControlURL/healthz" -TimeoutSec 5 | Out-Null
$headers = @{ 'X-AIPool-Client-Secret' = $ClientSecret }
$nodes = Invoke-RestMethod "$ControlURL/v1/nodes" -Headers $headers -TimeoutSec 5
Write-Host "Control plane is reachable; registered nodes: $($nodes.data.Count)" -ForegroundColor Green

if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'build.ps1')) { & (Join-Path $PSScriptRoot 'build.ps1') }
$env:AIPOOL_PROXY_ADDR = '127.0.0.1:11434'
$env:AIPOOL_CONTROL_URL = $ControlURL
$env:AIPOOL_CLIENT_SECRET = $ClientSecret
$env:AIPOOL_LOCAL_MODEL_DIR = $ModelDir
$env:AIPOOL_LOCAL_MODELS = $Models

Write-Host 'Local OpenAI-compatible API: http://127.0.0.1:11434/v1'
Write-Host "Local model directory: $ModelDir"
Write-Host 'The first request for a model uploads it automatically; subsequent requests reuse the SHA-256 cache.'
Write-Host 'Press Ctrl+C to stop.'
& $proxyBinary
