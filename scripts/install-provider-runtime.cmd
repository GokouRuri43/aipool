@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-provider-runtime.ps1" %*
exit /b %ERRORLEVEL%
