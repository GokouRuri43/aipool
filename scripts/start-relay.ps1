param(
    [string]$ListenAddress = '0.0.0.0:8443',
    [string]$Certificate = 'relay-certs\relay.crt',
    [string]$PrivateKey = 'relay-certs\relay.key'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'build.ps1')
$env:AIPOOL_RELAY_ADDR = $ListenAddress
$env:AIPOOL_RELAY_CERT = (Resolve-Path -LiteralPath (Join-Path $repoRoot $Certificate)).Path
$env:AIPOOL_RELAY_KEY = (Resolve-Path -LiteralPath (Join-Path $repoRoot $PrivateKey)).Path
& (Join-Path $repoRoot 'bin\relay.exe')
