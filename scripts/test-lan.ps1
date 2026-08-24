param(
    [string]$ProxyURL = 'http://127.0.0.1:11434',
    [string]$Model = 'qwen2.5-1.5b-instruct',
    [int]$FirstRequestTimeoutSec = 21600
)

$ErrorActionPreference = 'Stop'
$ProxyURL = $ProxyURL.TrimEnd('/')

$healthWatch = [Diagnostics.Stopwatch]::StartNew()
Invoke-RestMethod "$ProxyURL/healthz" -TimeoutSec 5 | Out-Null
$healthWatch.Stop()

$models = Invoke-RestMethod "$ProxyURL/v1/models" -TimeoutSec 10
if ($models.data.id -notcontains $Model) {
    throw "Model '$Model' is not advertised. Available: $($models.data.id -join ', ')"
}

$body = @{
    model = $Model
    messages = @(@{ role = 'user'; content = 'Reply with exactly: AIPool LAN OK' })
    max_tokens = 32
    stream = $false
} | ConvertTo-Json -Depth 5 -Compress

$inferenceWatch = [Diagnostics.Stopwatch]::StartNew()
$response = Invoke-RestMethod -Method Post -Uri "$ProxyURL/v1/chat/completions" -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec $FirstRequestTimeoutSec
$inferenceWatch.Stop()

$streamBody = @{
    model = $Model
    messages = @(@{ role = 'user'; content = 'Count from one to five.' })
    max_tokens = 32
    stream = $true
} | ConvertTo-Json -Depth 5 -Compress
$streamFile = Join-Path $env:TEMP ("aipool-stream-{0}.txt" -f [guid]::NewGuid())
try {
    $streamWatch = [Diagnostics.Stopwatch]::StartNew()
    $firstTokenSeconds = & curl.exe --silent --show-error --no-buffer --max-time 120 "$ProxyURL/v1/chat/completions" -H 'Content-Type: application/json' --data-binary $streamBody -o $streamFile --write-out '%{time_starttransfer}'
    if ($LASTEXITCODE -ne 0) { throw "curl streaming test failed with exit code $LASTEXITCODE" }
    $streamWatch.Stop()
    $streamText = Get-Content -LiteralPath $streamFile -Raw
    if ($streamText -notmatch 'data: \[DONE\]') { throw 'SSE stream did not terminate with [DONE].' }
} finally {
    Remove-Item -LiteralPath $streamFile -Force -ErrorAction SilentlyContinue
}

[pscustomobject]@{
    Status = 'PASS'
    ProxyHealthMs = $healthWatch.ElapsedMilliseconds
    NonStreamTotalMs = $inferenceWatch.ElapsedMilliseconds
    StreamTotalMs = $streamWatch.ElapsedMilliseconds
    FirstResponseByteMs = [math]::Round(([double]$firstTokenSeconds * 1000), 0)
    Reply = $response.choices[0].message.content
} | Format-List
