param(
    [Parameter(Mandatory = $true)]
    [string]$RelayAddress,
    [Parameter(Mandatory = $true)]
    [string]$RelayServerName,
    [Parameter(Mandatory = $true)]
    [string]$RelayFingerprint,
    [string]$PairFile = '',
	[string]$PoolDir = "$env:LOCALAPPDATA\AIPool\pool",
	[string]$ProviderName = 'friend-1',
	[switch]$AddProvider,
	[switch]$LocalProvider,
	[switch]$Distributed,
	[int]$DistributedMinNodes = 2,
	[int]$DistributedMaxNodes = 2,
	[string]$InstallDir = "$env:LOCALAPPDATA\AIPool",
    [string]$ModelDir = "$env:LOCALAPPDATA\AIPool\models",
    [string]$Models = '',
    [switch]$Mock
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1')
if ($Mock -and $Distributed) { throw '-Mock and -Distributed cannot be used together. Distributed inference requires the real llama.cpp runtime.' }
$repoRoot = Split-Path -Parent $PSScriptRoot
$controlBinary = Join-Path $PSScriptRoot 'control.exe'
$proxyBinary = Join-Path $PSScriptRoot 'proxy.exe'
$tunnelBinary = Join-Path $PSScriptRoot 'tunnel.exe'
$hostBinary = Join-Path $PSScriptRoot 'host.exe'
if (-not (Test-Path -LiteralPath $controlBinary)) { $controlBinary = Join-Path $repoRoot 'bin\control.exe' }
if (-not (Test-Path -LiteralPath $proxyBinary)) { $proxyBinary = Join-Path $repoRoot 'bin\proxy.exe' }
if (-not (Test-Path -LiteralPath $tunnelBinary)) { $tunnelBinary = Join-Path $repoRoot 'bin\tunnel.exe' }
if (-not (Test-Path -LiteralPath $hostBinary)) { $hostBinary = Join-Path $repoRoot 'bin\host.exe' }
if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'build.ps1')) { & (Join-Path $PSScriptRoot 'build.ps1') }

function Find-LlamaRuntime {
	foreach ($root in @((Join-Path $InstallDir 'runtime'),(Join-Path $PSScriptRoot 'runtime'))) {
		$binary = Get-ChildItem -LiteralPath $root -Filter 'llama-server.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName
		if ($binary) { return $binary }
	}
	return $null
}
function Find-RPCRuntime([string]$LlamaServerPath) {
	Get-ChildItem -LiteralPath (Split-Path -Parent $LlamaServerPath) -File -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Name -in @('ggml-rpc-server.exe','rpc-server.exe') } | Select-Object -First 1 -ExpandProperty FullName
}

function New-RandomSecret {
    $bytes = [byte[]]::new(32)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
}
New-Item -ItemType Directory -Force -Path $PoolDir | Out-Null
$poolConfigPath = Join-Path $PoolDir 'pool.json'
if (Test-Path -LiteralPath $poolConfigPath) {
    $pool = Get-Content -LiteralPath $poolConfigPath -Raw | ConvertFrom-Json
} else {
    $pool = [pscustomobject]@{ version = 1; client_secret = (New-RandomSecret); providers = @() }
}
if (-not $PairFile) { $PairFile = Join-Path ([Environment]::GetFolderPath('Desktop')) ("AIPool-Pair-{0}.json" -f $ProviderName) }
if (Test-Path -LiteralPath $PairFile) {
    $pair = Get-Content -LiteralPath $PairFile -Raw | ConvertFrom-Json
    if ($pair.version -ne 2) { throw 'This pair file uses the obsolete Relay authentication protocol. Delete it and generate a new pair file.' }
	if (@($pool.providers | Where-Object { $_.node_id -eq $pair.node_id }).Count -eq 0) {
		$pool.providers = @($pool.providers) + [pscustomobject]@{ node_id = $pair.node_id; host_secret = $pair.host_secret; lease_secret = $pair.lease_secret; scope = 'remote'; pair_file = $PairFile }
		$pool | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $poolConfigPath -Encoding UTF8
	}
} else {
	if ($ProviderName -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$') { throw 'ProviderName may only contain letters, digits, dot, underscore and hyphen.' }
	if ($ProviderName -eq 'local') { throw 'The reserved provider name local is created automatically with -LocalProvider.' }
	$nodeID = $ProviderName
	if (@($pool.providers | Where-Object { $_.node_id -eq $nodeID }).Count -gt 0) { throw "Provider '$nodeID' already exists in the pool." }
	$requesterHostPort = 28100 + @($pool.providers | Where-Object { $_.pair_file }).Count
	$requesterRPCPort = 28200 + @($pool.providers | Where-Object { $_.pair_file }).Count
    $pairToken = New-RandomSecret
    $pairID = (& $tunnelBinary pair-id $pairToken).Trim()
    if ($LASTEXITCODE -ne 0 -or $pairID.Length -ne 64) { throw 'Could not derive the Relay authentication public ID.' }
    $pair = [ordered]@{
        version = 2
        transport = 'aipool-relay'
        relay_address = $RelayAddress
        relay_server_name = $RelayServerName
        relay_fingerprint = $RelayFingerprint.ToLowerInvariant().Replace(':','')
        pair_id = $pairID
		node_id = $nodeID
		requester_host_port = $requesterHostPort
		requester_rpc_port = $requesterRPCPort
        pair_token = $pairToken
        tunnel_key = New-RandomSecret
        host_secret = New-RandomSecret
        lease_secret = New-RandomSecret
        created_at = (Get-Date).ToUniversalTime().ToString('o')
    }
    $pair | ConvertTo-Json | Set-Content -LiteralPath $PairFile -Encoding UTF8
	$pool.providers = @($pool.providers) + [pscustomobject]@{ node_id = $nodeID; host_secret = $pair.host_secret; lease_secret = $pair.lease_secret; scope = 'remote'; pair_file = $PairFile }
	$pool | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $poolConfigPath -Encoding UTF8
}
if ($LocalProvider -and @($pool.providers | Where-Object { $_.node_id -eq 'local' }).Count -eq 0) {
	$pool.providers = @($pool.providers) + [pscustomobject]@{ node_id = 'local'; host_secret = (New-RandomSecret); lease_secret = (New-RandomSecret); scope = 'local' }
	$pool | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $poolConfigPath -Encoding UTF8
}
if ($AddProvider) {
	Write-Host "Provider added to pool: $($pair.node_id)" -ForegroundColor Green
	Write-Host "Pair file: $PairFile"
	Write-Host 'Restart the running requester pool to activate the new provider.'
	exit 0
}
$migratedPairFiles = @()
$providerIndex = 0
foreach ($providerConfig in @($pool.providers | Where-Object { $_.pair_file })) {
	$providerPair = Get-Content -LiteralPath $providerConfig.pair_file -Raw | ConvertFrom-Json
	if (-not $providerPair.requester_rpc_port) {
		$derivedIndex = $providerIndex
		if ($providerPair.requester_host_port -and [int]$providerPair.requester_host_port -ge 28100) {
			$derivedIndex = [int]$providerPair.requester_host_port - 28100
		}
		$providerPair | Add-Member -NotePropertyName requester_rpc_port -NotePropertyValue (28200 + $derivedIndex) -Force
		$providerPair | ConvertTo-Json | Set-Content -LiteralPath $providerConfig.pair_file -Encoding UTF8
		$migratedPairFiles += $providerConfig.pair_file
	}
	$providerIndex++
}
$requiredPorts = @(28080,11434)
if ($LocalProvider) { $requiredPorts += @(28092,18081) }
$localRPCPort = 50053
if ($LocalProvider -and $Distributed) { $requiredPorts += $localRPCPort }
$requiredPorts += @($pool.providers | Where-Object { $_.pair_file } | ForEach-Object { $item=Get-Content -LiteralPath $_.pair_file -Raw | ConvertFrom-Json; @($item.requester_host_port,$item.requester_rpc_port) })
foreach ($port in $requiredPorts) {
    $listener = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($listener) { throw "TCP port $port is already used by PID $($listener.OwningProcess)." }
}

$env:AIPOOL_CONTROL_ADDR = '127.0.0.1:28080'
$env:AIPOOL_CONTROL_CONFIG = $poolConfigPath
$env:AIPOOL_CLIENT_SECRET = $pool.client_secret
$controlProcess = Start-Process -FilePath $controlBinary -WindowStyle Hidden -PassThru
$processes = @($controlProcess)
try {
	foreach ($providerConfig in @($pool.providers | Where-Object { $_.pair_file })) {
		$providerPair = Get-Content -LiteralPath $providerConfig.pair_file -Raw | ConvertFrom-Json
		$hostPort = [int]$providerPair.requester_host_port
		$rpcPort = [int]$providerPair.requester_rpc_port
		$env:AIPOOL_RELAY_ADDRESS = $providerPair.relay_address
		$env:AIPOOL_RELAY_SERVER_NAME = $providerPair.relay_server_name
		$env:AIPOOL_RELAY_FINGERPRINT = $providerPair.relay_fingerprint
		$env:AIPOOL_PAIR_ID = $providerPair.pair_id
		$env:AIPOOL_PAIR_TOKEN = $providerPair.pair_token
		$env:AIPOOL_TUNNEL_KEY = $providerPair.tunnel_key
		$env:AIPOOL_TUNNEL_ROLE = 'requester'
		$env:AIPOOL_TUNNEL_FORWARDS = "127.0.0.1:${hostPort}=provider-host,127.0.0.1:${rpcPort}=provider-rpc"
		$env:AIPOOL_TUNNEL_TARGETS = 'requester-control=127.0.0.1:28080'
		$processes += Start-Process -FilePath $tunnelBinary -WindowStyle Hidden -PassThru
	}
	if ($LocalProvider) {
		$runtimePath = Find-LlamaRuntime
		if (-not $Mock -and -not $runtimePath) { throw 'Local Provider runtime is missing.' }
		$local = @($pool.providers | Where-Object { $_.node_id -eq 'local' }) | Select-Object -First 1
		if (-not $local) { throw 'Could not create the local Provider credentials.' }
		$env:AIPOOL_HOST_ADDR='127.0.0.1:28092';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:28092';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:28080';$env:AIPOOL_NODE_ID='local';$env:AIPOOL_NODE_SCOPE='local';$env:AIPOOL_HOST_SECRET=$local.host_secret;$env:AIPOOL_LEASE_SECRET=$local.lease_secret
		if ($Distributed) {
			$localRPCBinary = Find-RPCRuntime $runtimePath
			if (-not $localRPCBinary) { throw 'The llama.cpp RPC server is missing from the local Provider runtime.' }
			$processes += Start-Process -FilePath $localRPCBinary -ArgumentList @('-H','127.0.0.1','-p',"$localRPCPort") -WindowStyle Hidden -PassThru
			$env:AIPOOL_STAGE_ENDPOINT = "127.0.0.1:${localRPCPort}"
		} else {
			Remove-Item Env:AIPOOL_STAGE_ENDPOINT -ErrorAction SilentlyContinue
		}
		if($Mock){$env:AIPOOL_MODELS='mock-llm';Remove-Item Env:AIPOOL_MODEL_CACHE_DIR -ErrorAction SilentlyContinue}else{$env:AIPOOL_MODEL_CACHE_DIR=Join-Path $InstallDir 'local-model-cache';$env:AIPOOL_MANAGED_RUNTIME=$runtimePath;Remove-Item Env:AIPOOL_MODELS -ErrorAction SilentlyContinue}
		$processes += Start-Process -FilePath $hostBinary -WindowStyle Hidden -PassThru
	}
    try {
        $env:AIPOOL_PROXY_ADDR = '127.0.0.1:11434'
        $env:AIPOOL_CONTROL_URL = 'http://127.0.0.1:28080'
		$env:AIPOOL_CLIENT_SECRET = $pool.client_secret
		if ($Mock) {
            Remove-Item Env:AIPOOL_LOCAL_MODEL_DIR -ErrorAction SilentlyContinue
            Remove-Item Env:AIPOOL_LOCAL_MODELS -ErrorAction SilentlyContinue
		} else {
			$env:AIPOOL_LOCAL_MODEL_DIR = $ModelDir
			$env:AIPOOL_LOCAL_MODELS = $Models
			if ($Distributed) {
				if ($DistributedMinNodes -lt 2 -or $DistributedMaxNodes -lt $DistributedMinNodes) { throw 'Distributed node limits are invalid.' }
				$runtimeServer = Find-LlamaRuntime
				if (-not $runtimeServer) { throw 'Requester llama-server.exe is required for distributed inference.' }
				$env:AIPOOL_DISTRIBUTED_LLAMA_SERVER = $runtimeServer
				$env:AIPOOL_DISTRIBUTED_MIN_NODES = "$DistributedMinNodes"
				$env:AIPOOL_DISTRIBUTED_MAX_NODES = "$DistributedMaxNodes"
				$env:AIPOOL_DISTRIBUTED_DEFAULT = '1'
			} else {
				Remove-Item Env:AIPOOL_DISTRIBUTED_LLAMA_SERVER -ErrorAction SilentlyContinue
				Remove-Item Env:AIPOOL_DISTRIBUTED_DEFAULT -ErrorAction SilentlyContinue
			}
		}
		Write-Host "Active providers: $(@($pool.providers).Count)" -ForegroundColor Cyan
		if ($migratedPairFiles.Count -gt 0) {
			Write-Warning 'Old Pair files were upgraded with distributed RPC ports. Send the updated Pair files to those Providers before distributed testing.'
			$migratedPairFiles | ForEach-Object { Write-Host "Updated Pair file: $_" -ForegroundColor Yellow }
		}
		if ($Distributed) {
			$expectedDistributedProviders = @($pool.providers | Where-Object { $_.pair_file }).Count + $(if ($LocalProvider) { 1 } else { 0 })
			if ($expectedDistributedProviders -lt $DistributedMinNodes) {
				throw "DistributedMinNodes=$DistributedMinNodes requires more configured Providers. Configure another friend or add -LocalProvider."
			}
		}
        Write-Host 'Local OpenAI-compatible API: http://127.0.0.1:11434/v1'
        Write-Host 'Press Ctrl+C to stop.'
        & $proxyBinary
    } finally {
		$processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
    }
} finally {
	$processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
}
