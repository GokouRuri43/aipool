param(
    [Parameter(Mandatory = $true)]
    [string]$HostName,
    [string]$OutputDir = 'relay-certs'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'build.ps1')
& (Join-Path $repoRoot 'bin\certgen.exe') -host $HostName -out (Join-Path $repoRoot $OutputDir)
