param([string]$OutputDir = 'dist')

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = Join-Path $repoRoot $OutputDir
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Assert-Package([string]$Path,[string[]]$Required,[switch]$Runtime) {
    if (-not (Test-Path -LiteralPath $Path)) { throw "Package not found: $Path" }
    $archive = [IO.Compression.ZipFile]::OpenRead($Path)
    try {
        $names = @($archive.Entries | ForEach-Object { $_.FullName.Replace('\','/') })
        foreach ($name in $Required) {
            if ($names -notcontains $name) { throw "$(Split-Path -Leaf $Path) is missing $name" }
        }
        if ($Runtime) {
            if ($names -notcontains 'runtime/llama-server.exe') { throw "$(Split-Path -Leaf $Path) is missing llama-server.exe" }
            if ($names -notcontains 'runtime/ggml-rpc-server.exe' -and $names -notcontains 'runtime/rpc-server.exe') { throw "$(Split-Path -Leaf $Path) is missing the RPC server" }
            if (@($names | Where-Object { $_.EndsWith('.zip') }).Count -gt 0) { throw "$(Split-Path -Leaf $Path) contains nested download ZIP files" }
        }
    } finally {
        $archive.Dispose()
    }
}

Assert-Package (Join-Path $outputRoot 'AIPool-Provider-Windows.zip') @('host.exe','tunnel.exe','start-provider-internet.ps1','normalize-windows-environment.ps1') -Runtime
Assert-Package (Join-Path $outputRoot 'AIPool-Requester-Windows.zip') @('control.exe','proxy.exe','tunnel.exe','start-requester-internet.ps1','test-internet.ps1','normalize-windows-environment.ps1') -Runtime
Write-Host 'AIPool Windows packages passed content verification.' -ForegroundColor Green
