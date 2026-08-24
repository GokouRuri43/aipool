# Windows environment variable names are case-insensitive, but a process
# launched by another runtime may contain both Path and PATH. Windows
# PowerShell Start-Process rejects that duplicate dictionary. Keep one value.
$processEnvironment = [Environment]::GetEnvironmentVariables('Process')
if ($processEnvironment.Contains('Path') -and $processEnvironment.Contains('PATH')) {
    $pathValue = [Environment]::GetEnvironmentVariable('Path','Process')
    [Environment]::SetEnvironmentVariable('PATH',$null,'Process')
    [Environment]::SetEnvironmentVariable('Path',$pathValue,'Process')
}
