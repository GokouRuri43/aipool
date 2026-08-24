@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0generate-relay-cert.ps1" %*
exit /b %ERRORLEVEL%
