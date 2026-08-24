@echo off
setlocal
echo Go:
go version
echo Node.js:
node --version
echo npm:
call npm.cmd --version
echo Rust:
rustc --version
echo Cargo:
cargo --version
echo Docker CLI:
docker --version
echo Docker engine:
docker info --format "{{.ServerVersion}}"
