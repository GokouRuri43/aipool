@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-provider-lan.ps1" %*
exit /b %ERRORLEVEL%
