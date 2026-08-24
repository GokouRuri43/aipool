@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-requester-lan.ps1" %*
exit /b %ERRORLEVEL%
