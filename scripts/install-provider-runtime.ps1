param(
    [string]$LlamaVersion = 'b10405',
    [string]$InstallDir = "$env:LOCALAPPDATA\AIPool",
    [ValidateSet('Auto','CUDA','CPU')]
    [string]$Variant = 'Auto'
)

$ErrorActionPreference = 'Stop'
$runtimeDir = Join-Path $InstallDir 'runtime'
$downloadDir = Join-Path $InstallDir 'downloads'
New-Item -ItemType Directory -Force -Path $runtimeDir,$downloadDir | Out-Null

function Download([string]$URL, [string]$Destination) {
    & curl.exe -L --fail --retry 10 --retry-delay 3 -C - -o $Destination $URL
    if ($LASTEXITCODE -ne 0) { throw "Download failed: $URL" }
}

if ($Variant -eq 'Auto') {
    $Variant = if (Get-Command nvidia-smi.exe -ErrorAction SilentlyContinue) { 'CUDA' } else { 'CPU' }
}
if ($Variant -eq 'CUDA') {
    $llamaZip = Join-Path $downloadDir "llama-$LlamaVersion-cuda.zip"
    $cudaZip = Join-Path $downloadDir "cudart-$LlamaVersion-cuda.zip"
    Download "https://github.com/ggml-org/llama.cpp/releases/download/$LlamaVersion/llama-$LlamaVersion-bin-win-cuda-12.4-x64.zip" $llamaZip
    Download "https://github.com/ggml-org/llama.cpp/releases/download/$LlamaVersion/cudart-llama-bin-win-cuda-12.4-x64.zip" $cudaZip
    Expand-Archive -LiteralPath $llamaZip -DestinationPath $runtimeDir -Force
    Expand-Archive -LiteralPath $cudaZip -DestinationPath $runtimeDir -Force
} else {
    $cpuZip = Join-Path $downloadDir "llama-$LlamaVersion-cpu.zip"
    Download "https://github.com/ggml-org/llama.cpp/releases/download/$LlamaVersion/llama-$LlamaVersion-bin-win-cpu-x64.zip" $cpuZip
    Expand-Archive -LiteralPath $cpuZip -DestinationPath $runtimeDir -Force
}

$server = Join-Path $runtimeDir 'llama-server.exe'
if (-not (Test-Path -LiteralPath $server)) { throw 'llama-server.exe was not found after provider runtime installation.' }
$rpcServer = @('ggml-rpc-server.exe','rpc-server.exe') | ForEach-Object { Join-Path $runtimeDir $_ } | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
if (-not $rpcServer) { throw 'The llama.cpp RPC server was not found after provider runtime installation.' }
& $server --version
& $rpcServer --version
Write-Host "Provider $Variant runtime installed. No model was downloaded." -ForegroundColor Green
