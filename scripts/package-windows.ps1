param(
    [string]$OutputDir = 'dist',
    [switch]$SkipProviderRuntime,
    [string]$InstallDir = "$env:LOCALAPPDATA\AIPool"
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = Join-Path $repoRoot $OutputDir
$provider = Join-Path $outputRoot 'AIPool-Provider-Windows'
$requester = Join-Path $outputRoot 'AIPool-Requester-Windows'
& (Join-Path $PSScriptRoot 'build.ps1')
New-Item -ItemType Directory -Force -Path $provider,$requester | Out-Null

Copy-Item (Join-Path $repoRoot 'bin\host.exe') $provider -Force
Copy-Item (Join-Path $repoRoot 'bin\tunnel.exe') $provider -Force
Copy-Item (Join-Path $repoRoot 'bin\stage.exe') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'start-provider-lan.ps1') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'start-provider-lan.cmd') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'install-provider-runtime.ps1') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'install-provider-runtime.cmd') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'start-provider-internet.ps1') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'start-provider-internet.cmd') $provider -Force
Copy-Item (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1') $provider -Force

Copy-Item (Join-Path $repoRoot 'bin\proxy.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\control.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\tunnel.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\host.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\stage.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\distributed.exe') $requester -Force
Copy-Item (Join-Path $repoRoot 'bin\ggufinspect.exe') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'start-requester-lan.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'start-requester-lan.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'test-lan.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'test-lan.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'start-requester-internet.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'start-requester-internet.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'test-internet.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'test-internet.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-real.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-real.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-pool-real.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-pool-real.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-relay-real.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'smoke-distributed-relay-real.cmd') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'install-provider-runtime.ps1') $requester -Force
Copy-Item (Join-Path $PSScriptRoot 'install-provider-runtime.cmd') $requester -Force

if (-not $SkipProviderRuntime) {
	$runtime = Join-Path $InstallDir 'runtime'
	$runtimeServer = Get-ChildItem -LiteralPath $runtime -Filter 'llama-server.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
	if (-not $runtimeServer) { throw "Provider runtime not found under $runtime. Run install-provider-runtime.cmd first." }
	$providerRuntime = Join-Path $provider 'runtime'
	$requesterRuntime = Join-Path $requester 'runtime'
	New-Item -ItemType Directory -Force -Path $providerRuntime,$requesterRuntime | Out-Null
	# Release archives sometimes leave downloaded ZIPs beside the binaries.
	# Packages need only the extracted runtime files.
	Get-ChildItem -LiteralPath $runtimeServer.Directory.FullName -File | Where-Object { $_.Extension -ne '.zip' } | ForEach-Object {
		Copy-Item -LiteralPath $_.FullName -Destination $providerRuntime -Force
		Copy-Item -LiteralPath $_.FullName -Destination $requesterRuntime -Force
	}
	$providerRPC = Get-ChildItem -LiteralPath $providerRuntime -File | Where-Object { $_.Name -in @('ggml-rpc-server.exe','rpc-server.exe') } | Select-Object -First 1
	if (-not $providerRPC) { throw 'Packaged runtime is missing the llama.cpp RPC server.' }
}

Compress-Archive -Path (Join-Path $provider '*') -DestinationPath (Join-Path $outputRoot 'AIPool-Provider-Windows.zip') -Force
Compress-Archive -Path (Join-Path $requester '*') -DestinationPath (Join-Path $outputRoot 'AIPool-Requester-Windows.zip') -Force
Get-Item (Join-Path $outputRoot '*.zip') | Select-Object FullName,Length
if (-not $SkipProviderRuntime) { & (Join-Path $PSScriptRoot 'verify-windows-package.ps1') -OutputDir $OutputDir }
