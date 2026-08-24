param(
    [string]$HostEndpoint = '',
    [string]$ControlURL = 'http://127.0.0.1:8080',
    [string]$HostSecret = 'lan-dev-host-change-me',
    [string]$ClientSecret = 'lan-dev-client-change-me',
    [string]$LeaseSecret = 'lan-dev-lease-change-me'
)

Write-Warning 'start-real-host is retained as a compatibility alias. Providers no longer select or install models.'
& (Join-Path $PSScriptRoot 'start-provider-lan.ps1') -HostEndpoint $HostEndpoint -ControlURL $ControlURL -HostSecret $HostSecret -ClientSecret $ClientSecret -LeaseSecret $LeaseSecret
exit $LASTEXITCODE
