@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0test-lan.ps1" %*
exit /b %ERRORLEVEL%
