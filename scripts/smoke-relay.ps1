$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'build.ps1')
$certDir = Join-Path $repoRoot '.cache\relay-smoke-certs'
New-Item -ItemType Directory -Force -Path $certDir | Out-Null
$certOutput = & (Join-Path $repoRoot 'bin\certgen.exe') -host '127.0.0.1' -out $certDir
$fingerprint = (($certOutput | Where-Object { $_ -like 'fingerprint_sha256=*' }) -split '=',2)[1]
if (-not $fingerprint) { throw 'Could not generate relay fingerprint.' }

function New-RandomSecret {
    $bytes = [byte[]]::new(32)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
}
$pairToken = New-RandomSecret
$pairID = (& (Join-Path $repoRoot 'bin\tunnel.exe') pair-id $pairToken).Trim()
$tunnelKey = New-RandomSecret
$hostSecret = New-RandomSecret
$clientSecret = New-RandomSecret
$leaseSecret = New-RandomSecret
$processes = @()
try {
    $env:AIPOOL_RELAY_ADDR='127.0.0.1:38443';$env:AIPOOL_RELAY_CERT=Join-Path $certDir 'relay.crt';$env:AIPOOL_RELAY_KEY=Join-Path $certDir 'relay.key'
    $processes += Start-Process (Join-Path $repoRoot 'bin\relay.exe') -WindowStyle Hidden -PassThru
    Start-Sleep -Milliseconds 200

    $env:AIPOOL_CONTROL_ADDR='127.0.0.1:38080';$env:AIPOOL_HOST_SECRET=$hostSecret;$env:AIPOOL_CLIENT_SECRET=$clientSecret;$env:AIPOOL_LEASE_SECRET=$leaseSecret
    $processes += Start-Process (Join-Path $repoRoot 'bin\control.exe') -WindowStyle Hidden -PassThru
    $common=@{AIPOOL_RELAY_ADDRESS='127.0.0.1:38443';AIPOOL_RELAY_SERVER_NAME='127.0.0.1';AIPOOL_RELAY_FINGERPRINT=$fingerprint;AIPOOL_PAIR_ID=$pairID;AIPOOL_PAIR_TOKEN=$pairToken;AIPOOL_TUNNEL_KEY=$tunnelKey}
    foreach($entry in $common.GetEnumerator()){[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}

    $env:AIPOOL_TUNNEL_ROLE='requester';$env:AIPOOL_TUNNEL_FORWARDS='127.0.0.1:38091=provider-host';$env:AIPOOL_TUNNEL_TARGETS='requester-control=127.0.0.1:38080'
    $processes += Start-Process (Join-Path $repoRoot 'bin\tunnel.exe') -WindowStyle Hidden -PassThru
    $env:AIPOOL_TUNNEL_ROLE='provider';$env:AIPOOL_TUNNEL_FORWARDS='127.0.0.1:38180=requester-control';$env:AIPOOL_TUNNEL_TARGETS='provider-host=127.0.0.1:38191'
    $processes += Start-Process (Join-Path $repoRoot 'bin\tunnel.exe') -WindowStyle Hidden -PassThru

    $env:AIPOOL_HOST_ADDR='127.0.0.1:38191';$env:AIPOOL_HOST_ENDPOINT='http://127.0.0.1:38091';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:38180';$env:AIPOOL_NODE_ID='smoke-provider';$env:AIPOOL_MODELS='mock-llm';$env:AIPOOL_HOST_SECRET=$hostSecret;$env:AIPOOL_LEASE_SECRET=$leaseSecret
    Remove-Item Env:AIPOOL_MODEL_CACHE_DIR -ErrorAction SilentlyContinue;Remove-Item Env:AIPOOL_RUNTIME_URL -ErrorAction SilentlyContinue
    $processes += Start-Process (Join-Path $repoRoot 'bin\host.exe') -WindowStyle Hidden -PassThru
    $env:AIPOOL_PROXY_ADDR='127.0.0.1:38134';$env:AIPOOL_CONTROL_URL='http://127.0.0.1:38080';$env:AIPOOL_CLIENT_SECRET=$clientSecret;Remove-Item Env:AIPOOL_LOCAL_MODEL_DIR -ErrorAction SilentlyContinue;Remove-Item Env:AIPOOL_LOCAL_MODELS -ErrorAction SilentlyContinue
    $processes += Start-Process (Join-Path $repoRoot 'bin\proxy.exe') -WindowStyle Hidden -PassThru

    $deadline=(Get-Date).AddSeconds(15)
    do {
        try {
            $body='{"model":"mock-llm","messages":[{"role":"user","content":"self-hosted relay smoke"}]}'
            $response=Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:38134/v1/chat/completions' -ContentType 'application/json' -Body $body -TimeoutSec 5
            if($response.choices[0].message.content -match 'self-hosted relay smoke'){break}
        } catch {}
        Start-Sleep -Milliseconds 300
    }while((Get-Date)-lt $deadline)
    if(-not $response){throw 'Self-hosted Relay smoke test timed out.'}
    $response | ConvertTo-Json -Depth 8
} finally {
    $processes | Where-Object {$_ -and -not $_.HasExited} | Stop-Process
}
