$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$cacheRoot = Join-Path $repoRoot '.cache'
$env:GOCACHE = Join-Path $cacheRoot 'go-build'
$env:GOMODCACHE = Join-Path $cacheRoot 'go-mod'
$env:GOPATH = Join-Path $cacheRoot 'gopath'

New-Item -ItemType Directory -Force -Path (Join-Path $repoRoot 'bin') | Out-Null

Push-Location $repoRoot
try {
    go test ./...
    go vet ./...
    go build -o bin/control.exe ./cmd/control
    go build -o bin/host.exe ./cmd/host
    go build -o bin/proxy.exe ./cmd/proxy
    go build -o bin/relay.exe ./cmd/relay
    go build -o bin/tunnel.exe ./cmd/tunnel
    go build -o bin/certgen.exe ./cmd/certgen
    go build -o bin/stage.exe ./cmd/stage
	go build -o bin/ggufinspect.exe ./cmd/ggufinspect
	go build -o bin/distributed.exe ./cmd/distributed

    $originalGoOS = $env:GOOS
    $originalGoArch = $env:GOARCH
    $originalCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        $env:CGO_ENABLED = '0'
        New-Item -ItemType Directory -Force -Path 'bin/linux' | Out-Null
        go build -trimpath -ldflags '-s -w' -o bin/linux/control ./cmd/control
        go build -trimpath -ldflags '-s -w' -o bin/linux/host ./cmd/host
        go build -trimpath -ldflags '-s -w' -o bin/linux/proxy ./cmd/proxy
        go build -trimpath -ldflags '-s -w' -o bin/linux/relay ./cmd/relay
        go build -trimpath -ldflags '-s -w' -o bin/linux/tunnel ./cmd/tunnel
        go build -trimpath -ldflags '-s -w' -o bin/linux/certgen ./cmd/certgen
		go build -trimpath -ldflags '-s -w' -o bin/linux/stage ./cmd/stage
		go build -trimpath -ldflags '-s -w' -o bin/linux/ggufinspect ./cmd/ggufinspect
		go build -trimpath -ldflags '-s -w' -o bin/linux/distributed ./cmd/distributed
    } finally {
        $env:GOOS = $originalGoOS
        $env:GOARCH = $originalGoArch
        $env:CGO_ENABLED = $originalCGO
    }
} finally {
    Pop-Location
}
