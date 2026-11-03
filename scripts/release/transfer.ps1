# Builds suzuri-transfer.exe (hato, libs/transfer) and puts it where the release
# overlay's bundle.externalBin expects it: packaging\bin\suzuri-transfer-<triple>.exe.
# The NSIS installer then puts it next to suzuri.exe, so transfers work with
# no Go suzuri installed (#330).
$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$triple = ((rustc -vV) | Select-String '^host: (.+)$').Matches[0].Groups[1].Value
cargo build --release --manifest-path (Join-Path $root "libs\transfer\Cargo.toml") -p hato-cli --bin suzuri-transfer
if ($LASTEXITCODE -ne 0) { throw "suzuri-transfer build failed: $LASTEXITCODE" }
$out = Join-Path $root "packaging\bin"
New-Item -ItemType Directory -Force $out | Out-Null
Copy-Item (Join-Path $root "libs\transfer\target\release\suzuri-transfer.exe") (Join-Path $out "suzuri-transfer-$triple.exe") -Force
Write-Host "==> packaging\bin\suzuri-transfer-$triple.exe"
