param(
    [string]$HostEndpoint = '',
    [string]$ControlURL = 'http://127.0.0.1:8080',
    [string]$InstallDir = "$env:LOCALAPPDATA\AIPool",
    [string]$HostSecret = 'lan-dev-host-change-me',
    [string]$ClientSecret = 'lan-dev-client-change-me',
    [string]$LeaseSecret = 'lan-dev-lease-change-me',
    [switch]$SkipControl
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1')
$repoRoot = Split-Path -Parent $PSScriptRoot
$hostBinary = Join-Path $PSScriptRoot 'host.exe'
$controlBinary = Join-Path $PSScriptRoot 'control.exe'
if (-not (Test-Path -LiteralPath $hostBinary)) { $hostBinary = Join-Path $repoRoot 'bin\host.exe' }
if (-not (Test-Path -LiteralPath $controlBinary)) { $controlBinary = Join-Path $repoRoot 'bin\control.exe' }
$runtimePath = Join-Path $PSScriptRoot 'runtime\llama-server.exe'
if (-not (Test-Path -LiteralPath $runtimePath)) {
    $runtimePath = Get-ChildItem -LiteralPath (Join-Path $InstallDir 'runtime') -Filter 'llama-server.exe' -File -Recurse -ErrorAction SilentlyContinue |
        Select-Object -First 1 -ExpandProperty FullName
}
$cacheDir = Join-Path $InstallDir 'host-model-cache'

function Get-LanIPv4 {
    $candidate = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop |
        Where-Object {
            $_.IPAddress -ne '127.0.0.1' -and
            $_.IPAddress -match '^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)' -and
            $_.InterfaceAlias -notmatch 'vEthernet|WSL|Docker|VMware|VirtualBox|Tailscale|ZeroTier'
        } |
        Sort-Object InterfaceMetric, SkipAsSource |
        Select-Object -First 1
    if (-not $candidate) { throw 'No physical private IPv4 address was found. Pass -HostEndpoint explicitly.' }
    return $candidate.IPAddress
}

function Assert-TcpPortFree([int]$Port, [string]$Service) {
    $listener = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($listener) { throw "TCP port $Port is already used by PID $($listener.OwningProcess); stop it before starting $Service." }
}

if (-not $runtimePath -or -not (Test-Path -LiteralPath $runtimePath)) {
    throw "AIPool managed runtime is missing at $runtimePath. Install the complete AIPool provider package; providers do not need to download models or configure llama.cpp manually."
}
$lanIP = $null
try { $lanIP = Get-LanIPv4 } catch {
    if (-not $HostEndpoint) { throw }
    $lanIP = ([uri]$HostEndpoint).Host
}
if (-not $HostEndpoint) { $HostEndpoint = "http://${lanIP}:8091" }
if (-not $SkipControl) { Assert-TcpPortFree 8080 'AIPool Control' }
Assert-TcpPortFree 8091 'AIPool Host'
Assert-TcpPortFree 18081 'AIPool managed runtime'
if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'build.ps1')) { & (Join-Path $PSScriptRoot 'build.ps1') }
New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null

$processes = @()
try {
    if (-not $SkipControl) {
        $env:AIPOOL_CONTROL_ADDR = '0.0.0.0:8080'
        $env:AIPOOL_HOST_SECRET = $HostSecret
        $env:AIPOOL_CLIENT_SECRET = $ClientSecret
        $env:AIPOOL_LEASE_SECRET = $LeaseSecret
        $processes += Start-Process -FilePath $controlBinary -WindowStyle Hidden -PassThru
    }

    $env:AIPOOL_HOST_ADDR = '0.0.0.0:8091'
    $env:AIPOOL_NODE_ID = $env:COMPUTERNAME + '-provider'
    $env:AIPOOL_HOST_ENDPOINT = $HostEndpoint
    $env:AIPOOL_CONTROL_URL = if ($SkipControl) { $ControlURL } else { 'http://127.0.0.1:8080' }
    $env:AIPOOL_HOST_SECRET = $HostSecret
    $env:AIPOOL_LEASE_SECRET = $LeaseSecret
    $env:AIPOOL_MODEL_CACHE_DIR = $cacheDir
    $env:AIPOOL_MANAGED_RUNTIME = $runtimePath
    Remove-Item Env:AIPOOL_MODELS -ErrorAction SilentlyContinue
    Remove-Item Env:AIPOOL_RUNTIME_URL -ErrorAction SilentlyContinue

    Write-Host ''
    Write-Host 'AIPool provider is ready; no model is installed on this computer.' -ForegroundColor Green
    if (-not $SkipControl) { Write-Host "Requester control URL: http://${lanIP}:8080" } else { Write-Host "Control URL: $ControlURL" }
    Write-Host "Advertised Host endpoint: $HostEndpoint"
    Write-Host "Models will be received automatically into the managed cache: $cacheDir"
    Write-Host 'If another LAN computer cannot connect, run this once in an Administrator terminal:' -ForegroundColor Yellow
    Write-Host 'New-NetFirewallRule -DisplayName "AIPool LAN" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8080,8091 -Profile Private -RemoteAddress LocalSubnet'
    Write-Host 'Do not expose these ports to the public Internet. Press Ctrl+C to stop.'
    & $hostBinary
} finally {
    $processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
}
