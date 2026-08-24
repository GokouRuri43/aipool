param(
    [Parameter(Mandatory = $true)]
    [string]$PairFile,
    [string]$InstallDir = "$env:LOCALAPPDATA\AIPool",
    [switch]$Mock
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1')
$repoRoot = Split-Path -Parent $PSScriptRoot
$hostBinary = Join-Path $PSScriptRoot 'host.exe'
$tunnelBinary = Join-Path $PSScriptRoot 'tunnel.exe'
if (-not (Test-Path -LiteralPath $hostBinary)) { $hostBinary = Join-Path $repoRoot 'bin\host.exe' }
if (-not (Test-Path -LiteralPath $tunnelBinary)) { $tunnelBinary = Join-Path $repoRoot 'bin\tunnel.exe' }
if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'build.ps1')) { & (Join-Path $PSScriptRoot 'build.ps1') }

$pair = Get-Content -LiteralPath $PairFile -Raw | ConvertFrom-Json
if ($pair.version -ne 2 -or $pair.transport -ne 'aipool-relay' -or -not $pair.node_id -or -not $pair.pair_id -or -not $pair.pair_token -or -not $pair.tunnel_key -or -not $pair.host_secret -or -not $pair.lease_secret) {
    throw 'The AIPool pair file is invalid or unsupported.'
}
$runtimePath = Get-ChildItem -LiteralPath (Join-Path $InstallDir 'runtime') -Filter 'llama-server.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName
if (-not $runtimePath) {
    $runtimePath = Join-Path $PSScriptRoot 'runtime\llama-server.exe'
}
if (-not $Mock -and (-not $runtimePath -or -not (Test-Path -LiteralPath $runtimePath))) {
    throw 'The Provider runtime is missing. Use the complete Provider package or add -Mock for network-only testing.'
}
$controlPort = if ($pair.provider_control_port) { [int]$pair.provider_control_port } else { 28080 }
$hostPort = if ($pair.provider_host_port) { [int]$pair.provider_host_port } else { 28091 }
$requesterHostPort = if ($pair.requester_host_port) { [int]$pair.requester_host_port } else { 28091 }
$requesterRPCPort = if ($pair.requester_rpc_port) {
    [int]$pair.requester_rpc_port
} elseif ($requesterHostPort -ge 28100) {
    28200 + ($requesterHostPort - 28100)
} else {
    28200
}
$rpcPort = if ($pair.provider_rpc_port) { [int]$pair.provider_rpc_port } else { 50052 }
foreach ($port in $controlPort,$hostPort,$rpcPort) {
    $listener = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($listener) { throw "TCP port $port is already used by PID $($listener.OwningProcess)." }
}

$env:AIPOOL_RELAY_ADDRESS = $pair.relay_address
$env:AIPOOL_RELAY_SERVER_NAME = $pair.relay_server_name
$env:AIPOOL_RELAY_FINGERPRINT = $pair.relay_fingerprint
$env:AIPOOL_PAIR_ID = $pair.pair_id
$env:AIPOOL_PAIR_TOKEN = $pair.pair_token
$env:AIPOOL_TUNNEL_KEY = $pair.tunnel_key
$env:AIPOOL_TUNNEL_ROLE = 'provider'
$env:AIPOOL_TUNNEL_FORWARDS = "127.0.0.1:${controlPort}=requester-control"
$env:AIPOOL_TUNNEL_TARGETS = "provider-host=127.0.0.1:${hostPort},provider-rpc=127.0.0.1:${rpcPort}"
$tunnelProcess = Start-Process -FilePath $tunnelBinary -WindowStyle Hidden -PassThru
try {
	$rpcProcess = $null
	if (-not $Mock) {
		$rpcBinary = Get-ChildItem -LiteralPath (Split-Path -Parent $runtimePath) -File -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Name -in @('ggml-rpc-server.exe','rpc-server.exe') } | Select-Object -First 1 -ExpandProperty FullName
		if (-not $rpcBinary) { throw 'The llama.cpp RPC server is missing from the Provider runtime.' }
		$rpcProcess = Start-Process -FilePath $rpcBinary -ArgumentList @('-H','127.0.0.1','-p',"$rpcPort") -WindowStyle Hidden -PassThru
	}
	$env:AIPOOL_HOST_ADDR = "127.0.0.1:${hostPort}"
	$env:AIPOOL_NODE_ID = $pair.node_id
	$env:AIPOOL_NODE_SCOPE = 'remote'
	$env:AIPOOL_HOST_ENDPOINT = "http://127.0.0.1:${requesterHostPort}"
	$env:AIPOOL_CONTROL_URL = "http://127.0.0.1:${controlPort}"
	if ($Mock) { Remove-Item Env:AIPOOL_STAGE_ENDPOINT -ErrorAction SilentlyContinue } else { $env:AIPOOL_STAGE_ENDPOINT = "127.0.0.1:${requesterRPCPort}" }
    $env:AIPOOL_HOST_SECRET = $pair.host_secret
    $env:AIPOOL_LEASE_SECRET = $pair.lease_secret
    Remove-Item Env:AIPOOL_RUNTIME_URL -ErrorAction SilentlyContinue
    if ($Mock) {
        Remove-Item Env:AIPOOL_MODEL_CACHE_DIR -ErrorAction SilentlyContinue
        Remove-Item Env:AIPOOL_MANAGED_RUNTIME -ErrorAction SilentlyContinue
        $env:AIPOOL_MODELS = 'mock-llm'
    } else {
        $cacheDir = Join-Path $InstallDir 'host-model-cache'
        New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null
        $env:AIPOOL_MODEL_CACHE_DIR = $cacheDir
        $env:AIPOOL_MANAGED_RUNTIME = $runtimePath
        Remove-Item Env:AIPOOL_MODELS -ErrorAction SilentlyContinue
    }
    Write-Host 'AIPool Provider is connecting to the requester through the self-hosted relay.' -ForegroundColor Green
    Write-Host 'No public IP, router port mapping, Tailscale account or third-party online service is required.'
    Write-Host 'Press Ctrl+C to stop.'
    & $hostBinary
} finally {
	if ($rpcProcess -and -not $rpcProcess.HasExited) { Stop-Process -Id $rpcProcess.Id }
    if ($tunnelProcess -and -not $tunnelProcess.HasExited) { Stop-Process -Id $tunnelProcess.Id }
}
