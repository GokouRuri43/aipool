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
if (-not $llamaServer -or -not $rpcServer) { throw 'Complete llama.cpp runtime is required.' }
if (-not (Test-Path -LiteralPath $Model)) { throw "GGUF model not found: $Model" }
& (Join-Path $PSScriptRoot 'build.ps1')

$workDir = Join-Path $repoRoot '.cache\distributed-relay-real-smoke'
$certDir = Join-Path $workDir 'certs'
New-Item -ItemType Directory -Force -Path $certDir | Out-Null
$certOutput = & (Join-Path $repoRoot 'bin\certgen.exe') -host '127.0.0.1' -out $certDir
$fingerprint = (($certOutput | Where-Object { $_ -like 'fingerprint_sha256=*' }) -split '=',2)[1]
if (-not $fingerprint) { throw 'Could not generate Relay certificate fingerprint.' }

function New-RandomSecret {
    $bytes = [byte[]]::new(32)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
}
function Start-Tunnel([string]$Role,[string]$PairID,[string]$PairToken,[string]$TunnelKey,[string]$Forwards,[string]$Targets,[string]$Name) {
    $env:AIPOOL_RELAY_ADDRESS='127.0.0.1:48443';$env:AIPOOL_RELAY_SERVER_NAME='127.0.0.1';$env:AIPOOL_RELAY_FINGERPRINT=$fingerprint;$env:AIPOOL_PAIR_ID=$PairID;$env:AIPOOL_PAIR_TOKEN=$PairToken;$env:AIPOOL_TUNNEL_KEY=$TunnelKey;$env:AIPOOL_TUNNEL_ROLE=$Role;$env:AIPOOL_TUNNEL_FORWARDS=$Forwards;$env:AIPOOL_TUNNEL_TARGETS=$Targets
    Start-Process -FilePath (Join-Path $repoRoot 'bin\tunnel.exe') -RedirectStandardOutput (Join-Path $workDir "$Name.stdout.log") -RedirectStandardError (Join-Path $workDir "$Name.stderr.log") -WindowStyle Hidden -PassThru
}

$clientSecret = New-RandomSecret
$localHostSecret = New-RandomSecret;$localLeaseSecret = New-RandomSecret
$friendHostSecret = New-RandomSecret;$friendLeaseSecret = New-RandomSecret
$pairToken = New-RandomSecret;$tunnelKey = New-RandomSecret
$pairID = (& (Join-Path $repoRoot 'bin\tunnel.exe') pair-id $pairToken).Trim()
$controlConfig = Join-Path $workDir 'pool.json'
$config = @{version=1;client_secret=$clientSecret;providers=@(
    @{node_id='relay-local';host_secret=$localHostSecret;lease_secret=$localLeaseSecret;scope='local'},
    @{node_id='relay-friend';host_secret=$friendHostSecret;lease_secret=$friendLeaseSecret;scope='remote'}
)} | ConvertTo-Json -Depth 6
[IO.File]::WriteAllText($controlConfig,$config,[Text.UTF8Encoding]::new($true))

$processes = @()
try {
    $env:AIPOOL_RELAY_ADDR='127.0.0.1:48443';$env:AIPOOL_RELAY_CERT=Join-Path $certDir 'relay.crt';$env:AIPOOL_RELAY_KEY=Join-Path $certDir 'relay.key'
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\relay.exe') -RedirectStandardOutput (Join-Path $workDir 'relay.stdout.log') -RedirectStandardError (Join-Path $workDir 'relay.stderr.log') -WindowStyle Hidden -PassThru

    $env:AIPOOL_CONTROL_ADDR='127.0.0.1:48280';$env:AIPOOL_CONTROL_CONFIG=$controlConfig;$env:AIPOOL_CLIENT_SECRET=$clientSecret
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\control.exe') -RedirectStandardOutput (Join-Path $workDir 'control.stdout.log') -RedirectStandardError (Join-Path $workDir 'control.stderr.log') -WindowStyle Hidden -PassThru

    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50261') -RedirectStandardOutput (Join-Path $workDir 'rpc-local.stdout.log') -RedirectStandardError (Join-Path $workDir 'rpc-local.stderr.log') -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath $rpcServer -ArgumentList @('-H','127.0.0.1','-p','50262') -RedirectStandardOutput (Join-Path $workDir 'rpc-friend.stdout.log') -RedirectStandardError (Join-Path $workDir 'rpc-friend.stderr.log') -WindowStyle Hidden -PassThru

    $processes += Start-Tunnel 'requester' $pairID $pairToken $tunnelKey '127.0.0.1:49262=provider-rpc' 'requester-control=127.0.0.1:48280' 'tunnel-requester'
    $processes += Start-Tunnel 'provider' $pairID $pairToken $tunnelKey '127.0.0.1:49280=requester-control' 'provider-rpc=127.0.0.1:50262' 'tunnel-provider'

    $env:AIPOOL_HOST_ADDR='127.0.0.1:48291';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:48291';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:48280';$env:AIPOOL_NODE_ID='relay-local';$env:AIPOOL_NODE_SCOPE='local';$env:AIPOOL_HOST_SECRET=$localHostSecret;$env:AIPOOL_LEASE_SECRET=$localLeaseSecret;$env:AIPOOL_STAGE_ENDPOINT='127.0.0.1:50261';$env:AIPOOL_MODEL_CACHE_DIR=Join-Path $workDir 'local-cache';$env:AIPOOL_MANAGED_RUNTIME=$llamaServer;Remove-Item Env:AIPOOL_MODELS -ErrorAction SilentlyContinue
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\host.exe') -RedirectStandardOutput (Join-Path $workDir 'host-local.stdout.log') -RedirectStandardError (Join-Path $workDir 'host-local.stderr.log') -WindowStyle Hidden -PassThru

    $env:AIPOOL_HOST_ADDR='127.0.0.1:48292';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:48292';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:49280';$env:AIPOOL_NODE_ID='relay-friend';$env:AIPOOL_NODE_SCOPE='remote';$env:AIPOOL_HOST_SECRET=$friendHostSecret;$env:AIPOOL_LEASE_SECRET=$friendLeaseSecret;$env:AIPOOL_STAGE_ENDPOINT='127.0.0.1:49262';$env:AIPOOL_MODEL_CACHE_DIR=Join-Path $workDir 'friend-cache'
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\host.exe') -RedirectStandardOutput (Join-Path $workDir 'host-friend.stdout.log') -RedirectStandardError (Join-Path $workDir 'host-friend.stderr.log') -WindowStyle Hidden -PassThru

    $deadline=(Get-Date).AddSeconds(30)
    do {
        try {$nodes=Invoke-RestMethod 'http://127.0.0.1:48280/v1/nodes' -Headers @{'X-AIPool-Client-Secret'=$clientSecret} -TimeoutSec 2}catch{$nodes=$null}
        if(@($nodes.data|Where-Object{$_.distributed_ready}).Count -eq 2){break}
        Start-Sleep -Milliseconds 300
    }while((Get-Date)-lt $deadline)
    if(@($nodes.data|Where-Object{$_.distributed_ready}).Count -ne 2){throw 'Local and tunneled Provider did not both register.'}

    $env:AIPOOL_PROXY_ADDR='127.0.0.1:48334';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:48280';$env:AIPOOL_CLIENT_SECRET=$clientSecret;$env:AIPOOL_LOCAL_MODEL_DIR='';$env:AIPOOL_LOCAL_MODELS="relay-distributed=$Model";$env:AIPOOL_DISTRIBUTED_LLAMA_SERVER=$llamaServer;$env:AIPOOL_DISTRIBUTED_MIN_NODES='2';$env:AIPOOL_DISTRIBUTED_MAX_NODES='2';$env:AIPOOL_DISTRIBUTED_PORT='48382';$env:AIPOOL_DISTRIBUTED_DEFAULT='1'
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\proxy.exe') -RedirectStandardOutput (Join-Path $workDir 'proxy.stdout.log') -RedirectStandardError (Join-Path $workDir 'proxy.stderr.log') -WindowStyle Hidden -PassThru
    $deadline=(Get-Date).AddSeconds(30);do{try{Invoke-RestMethod 'http://127.0.0.1:48334/healthz' -TimeoutSec 2|Out-Null;$ready=$true}catch{$ready=$false};if($ready){break};Start-Sleep -Milliseconds 300}while((Get-Date)-lt $deadline)
    if(-not $ready){throw 'Proxy did not become ready.'}

    $body=@{model='relay-distributed';messages=@(@{role='user';content='Reply exactly: AIPool encrypted RPC OK'});max_tokens=16;temperature=0;stream=$false}|ConvertTo-Json -Depth 6 -Compress
    $watch=[Diagnostics.Stopwatch]::StartNew()
    $response=Invoke-WebRequest -UseBasicParsing -Method Post -Uri 'http://127.0.0.1:48334/v1/chat/completions' -Headers @{'X-AIPool-Execution'='distributed'} -ContentType 'application/json' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec $TimeoutSec
    $watch.Stop()
    $selected=@($response.Headers['X-AIPool-Nodes'] -split ',')
    if($response.StatusCode -ne 200 -or $selected -notcontains 'relay-local' -or $selected -notcontains 'relay-friend'){throw "Encrypted distributed request selected unexpected nodes: $($selected -join ', ')"}
    $friendLog=Get-Content -LiteralPath (Join-Path $workDir 'rpc-friend.stdout.log') -Raw
    if($friendLog -notmatch 'Accepted client connection'){throw 'Remote RPC worker did not receive tunneled RPC traffic.'}
    $payload=$response.Content|ConvertFrom-Json
    [pscustomobject]@{Status='PASS';Transport='Self-hosted TLS Relay + AES-256-GCM RPC tunnel';Nodes=($selected -join ', ');TotalMs=$watch.ElapsedMilliseconds;Reply=$payload.choices[0].message.content;Logs=$workDir}|Format-List
} catch {
    Write-Error $_
    Get-ChildItem -LiteralPath $workDir -Filter '*.log' -ErrorAction SilentlyContinue|ForEach-Object{Write-Host "--- $($_.FullName) ---" -ForegroundColor Yellow;Get-Content -LiteralPath $_.FullName -Tail 100}
    throw
} finally {
    $processes|Where-Object{$_ -and -not $_.HasExited}|Stop-Process
}
