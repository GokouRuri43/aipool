@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0smoke-distributed-real.ps1" %*
exit /b %errorlevel%
