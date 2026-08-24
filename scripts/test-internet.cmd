@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0test-internet.ps1" %*
exit /b %ERRORLEVEL%
