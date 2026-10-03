@echo off
chcp 65001 >nul
cd /d %~dp0
echo Baue bridge.exe ...
go build -trimpath -ldflags "-s -w" -o bridge.exe .
if errorlevel 1 (echo FEHLER beim Bauen) else (echo Fertig: bridge.exe)
pause
