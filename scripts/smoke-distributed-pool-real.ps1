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
& (Join-Path $PSScriptRoot 'build.ps1')

$workDir = Join-Path $repoRoot '.cache\distributed-pool-real-smoke'
New-Item -ItemType Directory -Force -Path $workDir | Out-Null
$controlConfig = Join-Path $workDir 'pool.json'
$clientSecret = 'smoke-client-secret'
$config = @{
    version = 1
    client_secret = $clientSecret
    providers = @(
        @{ node_id='smoke-local'; host_secret='local-host-secret'; lease_secret='local-lease-secret'; scope='local' },
        @{ node_id='smoke-friend'; host_secret='friend-host-secret'; lease_secret='friend-lease-secret'; scope='remote' }
    )
} | ConvertTo-Json -Depth 6
[IO.File]::WriteAllText($controlConfig, $config, [Text.UTF8Encoding]::new($true))

$processes = @()
try {
    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50161') -RedirectStandardOutput (Join-Path $workDir 'rpc-local.stdout.log') -RedirectStandardError (Join-Path $workDir 'rpc-local.stderr.log') -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50162') -RedirectStandardOutput (Join-Path $workDir 'rpc-friend.stdout.log') -RedirectStandardError (Join-Path $workDir 'rpc-friend.stderr.log') -WindowStyle Hidden -PassThru

    $env:AIPOOL_CONTROL_ADDR='127.0.0.1:48080';$env:AIPOOL_CONTROL_CONFIG=$controlConfig;$env:AIPOOL_CLIENT_SECRET=$clientSecret
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\control.exe') -RedirectStandardOutput (Join-Path $workDir 'control.stdout.log') -RedirectStandardError (Join-Path $workDir 'control.stderr.log') -WindowStyle Hidden -PassThru

    $env:AIPOOL_HOST_ADDR='127.0.0.1:48091';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:48091';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:48080';$env:AIPOOL_NODE_ID='smoke-local';$env:AIPOOL_NODE_SCOPE='local';$env:AIPOOL_HOST_SECRET='local-host-secret';$env:AIPOOL_LEASE_SECRET='local-lease-secret';$env:AIPOOL_STAGE_ENDPOINT='127.0.0.1:50161';$env:AIPOOL_MODEL_CACHE_DIR=Join-Path $workDir 'local-cache';$env:AIPOOL_MANAGED_RUNTIME=$llamaServer;Remove-Item Env:AIPOOL_MODELS -ErrorAction SilentlyContinue
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\host.exe') -RedirectStandardOutput (Join-Path $workDir 'host-local.stdout.log') -RedirectStandardError (Join-Path $workDir 'host-local.stderr.log') -WindowStyle Hidden -PassThru

    $env:AIPOOL_HOST_ADDR='127.0.0.1:48092';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:48092';$env:AIPOOL_NODE_ID='smoke-friend';$env:AIPOOL_NODE_SCOPE='remote';$env:AIPOOL_HOST_SECRET='friend-host-secret';$env:AIPOOL_LEASE_SECRET='friend-lease-secret';$env:AIPOOL_STAGE_ENDPOINT='127.0.0.1:50162';$env:AIPOOL_MODEL_CACHE_DIR=Join-Path $workDir 'friend-cache'
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\host.exe') -RedirectStandardOutput (Join-Path $workDir 'host-friend.stdout.log') -RedirectStandardError (Join-Path $workDir 'host-friend.stderr.log') -WindowStyle Hidden -PassThru

    $deadline = (Get-Date).AddSeconds(30)
    do {
        try { $nodes = Invoke-RestMethod 'http://127.0.0.1:48080/v1/nodes' -Headers @{'X-AIPool-Client-Secret'=$clientSecret} -TimeoutSec 2 } catch { $nodes = $null }
        if (@($nodes.data | Where-Object { $_.distributed_ready }).Count -eq 2) { break }
        Start-Sleep -Milliseconds 300
    } while ((Get-Date) -lt $deadline)
    if (@($nodes.data | Where-Object { $_.distributed_ready }).Count -ne 2) { throw 'Two distributed Providers did not register with Control.' }

    $env:AIPOOL_PROXY_ADDR='127.0.0.1:48134';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:48080';$env:AIPOOL_CLIENT_SECRET=$clientSecret;$env:AIPOOL_LOCAL_MODEL_DIR='';$env:AIPOOL_LOCAL_MODELS="distributed-smoke=$Model";$env:AIPOOL_DISTRIBUTED_LLAMA_SERVER=$llamaServer;$env:AIPOOL_DISTRIBUTED_MIN_NODES='2';$env:AIPOOL_DISTRIBUTED_MAX_NODES='2';$env:AIPOOL_DISTRIBUTED_PORT='48182';$env:AIPOOL_DISTRIBUTED_DEFAULT='1'
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\proxy.exe') -RedirectStandardOutput (Join-Path $workDir 'proxy.stdout.log') -RedirectStandardError (Join-Path $workDir 'proxy.stderr.log') -WindowStyle Hidden -PassThru

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    do {
        try { Invoke-RestMethod 'http://127.0.0.1:48134/healthz' -TimeoutSec 2 | Out-Null; $ready=$true } catch { $ready=$false }
        if ($ready) { break }
        if ($processes[-1].HasExited) { throw "AIPool Proxy exited with code $($processes[-1].ExitCode)." }
        Start-Sleep -Milliseconds 300
    } while ((Get-Date) -lt $deadline)
    if (-not $ready) { throw 'AIPool Proxy did not become ready.' }

    $body = @{model='distributed-smoke';messages=@(@{role='user';content='Reply with exactly: AIPool pool distributed OK'});max_tokens=16;temperature=0;stream=$false} | ConvertTo-Json -Depth 6 -Compress
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $response = Invoke-WebRequest -UseBasicParsing -Method Post -Uri 'http://127.0.0.1:48134/v1/chat/completions' -Headers @{'X-AIPool-Execution'='distributed'} -ContentType 'application/json' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec $TimeoutSec
    $watch.Stop()
    if ($response.StatusCode -ne 200 -or $response.Headers['X-AIPool-Execution'] -ne 'distributed') { throw 'Proxy did not complete the distributed request.' }
    $selected = @($response.Headers['X-AIPool-Nodes'] -split ',')
    if ($selected.Count -ne 2 -or $selected -notcontains 'smoke-local' -or $selected -notcontains 'smoke-friend') { throw "Unexpected distributed nodes: $($selected -join ', ')" }
    $payload = $response.Content | ConvertFrom-Json
    [pscustomobject]@{Status='PASS';Nodes=($selected -join ', ');TotalMs=$watch.ElapsedMilliseconds;Reply=$payload.choices[0].message.content;Logs=$workDir} | Format-List
} catch {
    Write-Error $_
    Get-ChildItem -LiteralPath $workDir -Filter '*.log' -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "--- $($_.FullName) ---" -ForegroundColor Yellow; Get-Content -LiteralPath $_.FullName -Tail 100 }
    throw
} finally {
    $processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
}
