param(
    [string]$LlamaVersion = 'b10405',
    [string]$ModelURL = 'https://hf-mirror.com/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf'
)

Write-Warning 'This is a requester-side development helper. Provider computers must use install-provider-runtime.cmd and never download a model manually.'

$ErrorActionPreference = 'Stop'
$base = Join-Path $env:LOCALAPPDATA 'AIPool'
$runtimeCache = Join-Path $base 'runtime'
$runtimeDir = Join-Path $runtimeCache "llama-$LlamaVersion-cuda12.4"
$modelDir = Join-Path $base 'models'
$llamaZip = Join-Path $runtimeCache "llama-$LlamaVersion-cuda12.4.zip"
$cudaZip = Join-Path $runtimeCache "cudart-llama-$LlamaVersion-cuda12.4.zip"
$model = Join-Path $modelDir 'qwen2.5-1.5b-instruct-q4_k_m.gguf'

New-Item -ItemType Directory -Force $runtimeCache,$runtimeDir,$modelDir | Out-Null

function Download([string]$URL, [string]$Destination) {
    & curl.exe -L --fail --retry 10 --retry-delay 3 -C - -o $Destination $URL
    if ($LASTEXITCODE -ne 0) { throw "Download failed: $URL" }
}

Download "https://github.com/ggml-org/llama.cpp/releases/download/$LlamaVersion/llama-$LlamaVersion-bin-win-cuda-12.4-x64.zip" $llamaZip
Download "https://github.com/ggml-org/llama.cpp/releases/download/$LlamaVersion/cudart-llama-bin-win-cuda-12.4-x64.zip" $cudaZip
Download $ModelURL $model

Expand-Archive -LiteralPath $llamaZip -DestinationPath $runtimeDir -Force
Expand-Archive -LiteralPath $cudaZip -DestinationPath $runtimeDir -Force

if (-not (Test-Path (Join-Path $runtimeDir 'llama-server.exe'))) {
    throw 'llama-server.exe was not found after extraction'
}

& (Join-Path $runtimeDir 'llama-server.exe') --version
Get-Item $model | Select-Object FullName,Length
