@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-provider-internet.ps1" %*
exit /b %ERRORLEVEL%
