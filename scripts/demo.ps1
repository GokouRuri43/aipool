$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'build.ps1')

$controlURL = 'http://127.0.0.1:18080'
$hostEndpoint = 'http://127.0.0.1:18090'
$proxyURL = 'http://127.0.0.1:18434'
$secret = 'demo-only-secret'
$processes = @()
try {
    $env:AIPOOL_CONTROL_ADDR = '127.0.0.1:18080'
    $env:AIPOOL_HOST_SECRET = $secret
    $env:AIPOOL_CLIENT_SECRET = $secret
    $env:AIPOOL_LEASE_SECRET = $secret
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\control.exe') -WindowStyle Hidden -PassThru
    Start-Sleep -Milliseconds 300

    $env:AIPOOL_HOST_ADDR = '127.0.0.1:18090'
    $env:AIPOOL_HOST_ENDPOINT = $hostEndpoint
    $env:AIPOOL_CONTROL_URL = $controlURL
    $env:AIPOOL_NODE_ID = 'demo-gpu'
    $env:AIPOOL_MODELS = 'mock-llm'
    Remove-Item Env:AIPOOL_RUNTIME_URL -ErrorAction SilentlyContinue
    Remove-Item Env:AIPOOL_RUNTIME_MODEL -ErrorAction SilentlyContinue
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\host.exe') -WindowStyle Hidden -PassThru
    Start-Sleep -Milliseconds 500

    $env:AIPOOL_PROXY_ADDR = '127.0.0.1:18434'
    $env:AIPOOL_CONTROL_URL = $controlURL
    $processes += Start-Process -FilePath (Join-Path $repoRoot 'bin\proxy.exe') -WindowStyle Hidden -PassThru
    Start-Sleep -Milliseconds 500

	foreach ($process in $processes) {
		if ($process.HasExited) { throw "$($process.ProcessName) failed to start; one of the demo ports may already be in use." }
	}

    $body = @{
        model = 'mock-llm'
        messages = @(@{ role = 'user'; content = 'AIPool end-to-end test' })
        stream = $false
    } | ConvertTo-Json -Depth 5

    Invoke-RestMethod -Method Post -Uri "$proxyURL/v1/chat/completions" -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body)) | ConvertTo-Json -Depth 8
} finally {
    $processes | Where-Object { $_ -and -not $_.HasExited } | Stop-Process
}
