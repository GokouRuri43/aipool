@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-docker-gpu-host.ps1" %*
exit /b %ERRORLEVEL%

