@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0smoke-distributed-pool-real.ps1" %*
exit /b %ERRORLEVEL%
