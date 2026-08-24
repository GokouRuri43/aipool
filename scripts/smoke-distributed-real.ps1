param(
    [string]$Model = "$env:LOCALAPPDATA\AIPool\models\qwen2.5-1.5b-instruct-q4_k_m.gguf",
    [string]$InstallDir = "$env:LOCALAPPDATA\AIPool",
    [int]$TimeoutSec = 600
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'normalize-windows-environment.ps1')
$runtimeDir = Join-Path $InstallDir 'runtime'
$llamaServer = Get-ChildItem -LiteralPath $runtimeDir -Filter 'llama-server.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName
$rpcServer = Get-ChildItem -LiteralPath $runtimeDir -File -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Name -in @('ggml-rpc-server.exe','rpc-server.exe') } | Select-Object -First 1 -ExpandProperty FullName
if (-not $llamaServer -or -not $rpcServer) { throw 'Complete llama.cpp runtime with llama-server and RPC server is required.' }
if (-not (Test-Path -LiteralPath $Model)) { throw "GGUF model not found: $Model" }

$processes = @()
$logDir = Join-Path $repoRoot '.cache\distributed-real-smoke'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$rpc1Out = Join-Path $logDir 'rpc-50061.stdout.log'
$rpc1Err = Join-Path $logDir 'rpc-50061.stderr.log'
$rpc2Out = Join-Path $logDir 'rpc-50062.stdout.log'
$rpc2Err = Join-Path $logDir 'rpc-50062.stderr.log'
$llamaOut = Join-Path $logDir 'llama-server.stdout.log'
$llamaErr = Join-Path $logDir 'llama-server.stderr.log'
Remove-Item -LiteralPath $rpc1Out,$rpc1Err,$rpc2Out,$rpc2Err,$llamaOut,$llamaErr -Force -ErrorAction SilentlyContinue
try {
    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50061') -RedirectStandardOutput $rpc1Out -RedirectStandardError $rpc1Err -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50062') -RedirectStandardOutput $rpc2Out -RedirectStandardError $rpc2Err -WindowStyle Hidden -PassThru
    Start-Sleep -Seconds 2
    if (@($processes | Where-Object { $_.HasExited }).Count -gt 0) { throw 'One or more llama.cpp RPC workers exited during startup.' }
    $arguments = @('--model',$Model,'--host','127.0.0.1','--port','18083','--ctx-size','512','--n-gpu-layers','999','--rpc','127.0.0.1:50061,127.0.0.1:50062','--split-mode','layer','--tensor-split','1,1')
    $processes += Start-Process -FilePath $llamaServer -ArgumentList $arguments -RedirectStandardOutput $llamaOut -RedirectStandardError $llamaErr -WindowStyle Hidden -PassThru
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    do {
        if ($processes[-1].HasExited) { throw "Distributed llama-server exited with code $($processes[-1].ExitCode)." }
        try { $health = Invoke-RestMethod 'http://127.0.0.1:18083/health' -TimeoutSec 2 } catch { $health = $null }
        if ($health.status -eq 'ok') { break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    if ($health.status -ne 'ok') { throw 'Distributed llama-server did not become healthy.' }
    $body = @{ model='distributed-smoke'; messages=@(@{role='user';content='Reply with exactly: AIPool distributed OK'}); max_tokens=16; temperature=0; stream=$false } | ConvertTo-Json -Depth 6 -Compress
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $response = Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:18083/v1/chat/completions' -ContentType 'application/json' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec $TimeoutSec
    $watch.Stop()
    foreach ($worker in @(@{Endpoint='127.0.0.1:50061';Log=$rpc1Out},@{Endpoint='127.0.0.1:50062';Log=$rpc2Out})) {
        $workerLog = Get-Content -LiteralPath $worker.Log -Raw -ErrorAction SilentlyContinue
        if ($workerLog -notmatch [regex]::Escape($worker.Endpoint) -or $workerLog -notmatch 'Accepted client connection') {
            throw "RPC worker $($worker.Endpoint) did not confirm serving the distributed runtime."
        }
    }
    [pscustomobject]@{ Status='PASS'; RPCWorkers=2; Model=$Model; TotalMs=$watch.ElapsedMilliseconds; Reply=$response.choices[0].message.content; Logs=$logDir } | Format-List
} catch {
    Write-Error $_
    foreach ($log in $rpc1Out,$rpc1Err,$rpc2Out,$rpc2Err,$llamaOut,$llamaErr) {
        if (Test-Path -LiteralPath $log) {
            Write-Host "--- $log ---" -ForegroundColor Yellow
            Get-Content -LiteralPath $log -Tail 100
        }
    }
    throw
} finally {
    $processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
}
