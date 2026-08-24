param(
    [string]$ProxyURL = 'http://127.0.0.1:11434',
    [string]$Model = 'mock-llm',
	[int]$FirstRequestTimeoutSec = 21600,
	[int]$Parallel = 1,
	[string]$NodeID = '',
	[ValidateSet('','local','remote')][string]$Scope = '',
	[ValidateSet('','single','distributed','auto')][string]$Execution = ''
)

$ErrorActionPreference = 'Stop'
$ProxyURL = $ProxyURL.TrimEnd('/')
$healthWatch = [Diagnostics.Stopwatch]::StartNew()
Invoke-RestMethod "$ProxyURL/healthz" -TimeoutSec 5 | Out-Null
$healthWatch.Stop()
$models = Invoke-RestMethod "$ProxyURL/v1/models" -TimeoutSec 30
if ($models.data.id -notcontains $Model) { throw "Model '$Model' is unavailable. Available: $($models.data.id -join ', ')" }

$body = @{
    model = $Model
    messages = @(@{ role = 'user'; content = 'Reply with exactly: AIPool Relay OK' })
    max_tokens = 32
    stream = $false
} | ConvertTo-Json -Depth 5 -Compress
$plainWatch = [Diagnostics.Stopwatch]::StartNew()
$requestHeaders = @{}
if ($NodeID) { $requestHeaders['X-AIPool-Node-ID'] = $NodeID }
if ($Scope) { $requestHeaders['X-AIPool-Scope'] = $Scope }
if ($Execution) { $requestHeaders['X-AIPool-Execution'] = $Execution }
$plainResponse = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$ProxyURL/v1/chat/completions" -Headers $requestHeaders -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec $FirstRequestTimeoutSec
$response = $plainResponse.Content | ConvertFrom-Json
$plainWatch.Stop()
if ($Execution -eq 'distributed' -and $plainResponse.Headers['X-AIPool-Execution'] -ne 'distributed') { throw 'Proxy did not confirm distributed execution.' }

$streamBody = @{
    model = $Model
    messages = @(@{ role = 'user'; content = 'Count from one to five.' })
    max_tokens = 32
    stream = $true
} | ConvertTo-Json -Depth 5 -Compress
$streamFile = Join-Path $env:TEMP ("aipool-relay-stream-{0}.txt" -f [guid]::NewGuid())
try {
    $streamWatch = [Diagnostics.Stopwatch]::StartNew()
    $curlArgs = @('--silent','--show-error','--no-buffer','--max-time',"$FirstRequestTimeoutSec","$ProxyURL/v1/chat/completions",'-H','Content-Type: application/json')
    foreach ($header in $requestHeaders.GetEnumerator()) { $curlArgs += @('-H',("{0}: {1}" -f $header.Key,$header.Value)) }
    $curlArgs += @('--data-binary',$streamBody,'-o',$streamFile)
    & curl.exe @curlArgs
    if ($LASTEXITCODE -ne 0) { throw "curl streaming test failed with exit code $LASTEXITCODE" }
    $streamWatch.Stop()
    if ((Get-Content -LiteralPath $streamFile -Raw) -notmatch 'data: \[DONE\]') { throw 'SSE stream did not terminate with [DONE].' }
} finally {
    Remove-Item -LiteralPath $streamFile -Force -ErrorAction SilentlyContinue
}

$parallelNodes = @()
if ($Parallel -gt 1) {
	$jobs = 1..$Parallel | ForEach-Object {
		Start-Job -ScriptBlock {
			param($url,$payload,$timeout,$headers)
			$response = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$url/v1/chat/completions" -Headers $headers -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($payload)) -TimeoutSec $timeout
			[pscustomobject]@{ NodeID = $response.Headers['X-AIPool-Node-ID']; Status = $response.StatusCode }
		} -ArgumentList $ProxyURL,$body,$FirstRequestTimeoutSec,$requestHeaders
	}
	try { $parallelNodes = @($jobs | Wait-Job | Receive-Job) } finally { $jobs | Remove-Job -Force }
	if (@($parallelNodes | Where-Object { $_.Status -ne 200 }).Count -gt 0) { throw 'One or more parallel pool requests failed.' }
}

[pscustomobject]@{
    Status = 'PASS'
    Transport = 'AIPool self-hosted TLS relay + end-to-end AES-256-GCM'
    ProxyHealthMs = $healthWatch.ElapsedMilliseconds
    FirstRequestTotalMs = $plainWatch.ElapsedMilliseconds
    StreamTotalMs = $streamWatch.ElapsedMilliseconds
    Reply = $response.choices[0].message.content
	ParallelRequests = $Parallel
	SelectedNodes = (($parallelNodes | ForEach-Object { $_.NodeID }) -join ', ')
	Execution = $plainResponse.Headers['X-AIPool-Execution']
	DistributedNodes = $plainResponse.Headers['X-AIPool-Nodes']
} | Format-List
