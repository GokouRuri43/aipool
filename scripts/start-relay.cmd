@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-relay.ps1" %*
exit /b %ERRORLEVEL%
