# Done-when 1 and 4 (card hearth#923), the Windows twin of update-check.sh:
# Suzuri's real Install and restart (the app runs the NSIS installer and
# exits; the installer replaces suzuri.exe and starts it again), between
# two release builds, on a scratch install. A shell running a program in
# the old version survives the update; the new version shows it with its
# screen and scrollback; its host still runs the old build while a new pane
# gets the new one.
#
# Kept apart from the installed suzuri: the scratch builds are another
# product (suzuri-keepertest: their own install folder, Add/Remove Programs
# key, app id and WebView data) without the Go-migration installer hooks
# (they would pick suzuri's own install folder); the app runs with the
# scratch LOCALAPPDATA, RSZ_CONFIG_DIR and keeper folder (lib.ps1). A
# throwaway updater key and an http feed on 127.0.0.1, as in
# scripts/release/test-update-windows.ps1; nothing is published. The update
# installs quietly (/S, the same installer with no window), so a stuck
# install never leaves a dialog on the desktop; while it runs, every process
# that runs a program from the scratch install, and the installer, is
# printed, so a suzuri.exe the installer could not replace shows who held it.
#   powershell -ExecutionPolicy Bypass -File scripts\keeper\update-check.ps1 [-Old 0.10.0] [-New 0.10.1] [-Port 8787] [-SkipBuild]
param([string]$Old = "0.10.0", [string]$New = "0.10.1", [int]$Port = 8787, [switch]$SkipBuild)
. (Join-Path $PSScriptRoot "lib.ps1")
$Product = "suzuri-keepertest"
$UninstKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$Product"
$Inst = Join-Path $S "inst"
$before = (Real-Stamp) -join "; "
$utf8 = New-Object Text.UTF8Encoding $false
if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) { Fail "port $Port is in use" }
if (Test-Path $UninstKey) { Fail "$Product is installed already (an earlier run's?): uninstall it first" }

$keyDir = Join-Path $Root "dist\keeper-test"
$key = Join-Path $keyDir "test.key"
if (-not $SkipBuild) {
  New-Item -ItemType Directory -Force $keyDir | Out-Null
  Remove-Item "$key*" -ErrorAction SilentlyContinue
  cargo tauri signer generate --ci -w $key | Out-Null
  if (-not (Test-Path "$key.pub")) { Fail "signer generate failed" }
  $overlay = @{
    productName = $Product; mainBinaryName = "suzuri"; identifier = "com.stephenshorton.suzuri.keepertest"
    bundle = @{ windows = @{ nsis = @{ installerHooks = $null } } }
    plugins = @{ updater = @{ pubkey = (Get-Content "$key.pub" -Raw).Trim(); dangerousInsecureTransportProtocol = $true
                              windows = @{ installMode = "quiet" } } }
  } | ConvertTo-Json -Depth 8 -Compress
  [IO.File]::WriteAllText((Join-Path $keyDir "overlay.json"), $overlay, $utf8)
  foreach ($v in @($Old, $New)) {
    Say "== build $v ($Product)"
    & (Join-Path $Root "scripts\release\build-windows.ps1") -Version $v -NoUpload -ExtraConfig (Join-Path $keyDir "overlay.json")
  }
}
$distOld = Join-Path $Root "dist\$Old"; $distNew = Join-Path $Root "dist\$New"
$oldSetup = Get-ChildItem $distOld -Filter "*_${Old}_x64-setup.exe" | Select-Object -First 1
$newSetup = Get-ChildItem $distNew -Filter "*_${New}_x64-setup.exe" | Select-Object -First 1
if (-not $oldSetup -or -not $newSetup) { Fail "no installers in dist\$Old and dist\$New" }
cargo tauri signer sign -f $key "--password=" $newSetup.FullName | Out-Null
$sig = (Get-Content "$($newSetup.FullName).sig" -Raw).Trim()
$entry = @{ signature = $sig; url = "http://127.0.0.1:$Port/$($newSetup.Name)" }
$feed = @{ version = $New; notes = "keeper test"; pub_date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
           platforms = @{ "windows-x86_64" = $entry; "windows-x86_64-nsis" = $entry } } | ConvertTo-Json -Depth 6
[IO.File]::WriteAllText((Join-Path $distNew "latest.json"), $feed, $utf8)

$serve = Join-Path $S "serve.ps1"
[IO.File]::WriteAllText($serve, @'
param($dir, $port)
$l = [Net.HttpListener]::new(); $l.Prefixes.Add("http://127.0.0.1:$port/"); $l.Start()
while ($true) {
  $c = $l.GetContext(); $f = Join-Path $dir ($c.Request.Url.AbsolutePath.TrimStart("/"))
  if (Test-Path $f -PathType Leaf) { $b = [IO.File]::ReadAllBytes($f); $c.Response.ContentLength64 = $b.Length; $c.Response.OutputStream.Write($b, 0, $b.Length) }
  else { $c.Response.StatusCode = 404 }
  $c.Response.Close()
}
'@, $utf8)
$srv = Start-Process powershell -WindowStyle Hidden -PassThru -ArgumentList @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $serve, "-dir", $distNew, "-port", $Port)
Start-Sleep 2

function Ver { (Get-ItemProperty $UninstKey -ErrorAction SilentlyContinue).DisplayVersion }
function Exe-Ver { (Get-Item (Join-Path $Inst "suzuri.exe")).VersionInfo.ProductVersion }
function App-From-Inst { Get-CimInstance Win32_Process -Filter "Name='suzuri.exe'" | Where-Object { $_.ExecutablePath -ieq (Join-Path $Inst "suzuri.exe") } }
try {
  Say "== install $Old silently into $Inst"
  Start-Process $oldSetup.FullName -ArgumentList "/S", "/D=$Inst" -Wait
  if ((Ver) -ne $Old) { Fail "install of $Old not found under $UninstKey" }
  $loc = (Get-ItemProperty $UninstKey).InstallLocation.Trim('"')
  Say "installed $Product $(Ver) at $loc (suzuri.exe $(Exe-Ver)); the feed offers $New"

  $app1 = Start-App (Join-Path $Inst "suzuri.exe") @{ RSZ_UPDATE_AUTO = "1"; RSZ_UPDATE_URL = "http://127.0.0.1:$Port/latest.json" }
  $k = $null
  for ($i = 0; $i -lt 40 -and -not $k; $i++) { Start-Sleep -Milliseconds 500; $k = Kept | Select-Object -First 1 }
  if (-not $k -or $k.error) { Fail "no kept shell: $k" }
  $id = [int]$k.id; $hostPid = [int]$k.pid; $shell = [int]$k.shell_pid
  & $Keeper --dir $KDir type $id '1..400 | ForEach-Object { echo scroll-$_ }; $i = 0; while ($true) { $i++; echo tick-$i; Start-Sleep 1 }\n'
  Say "app $app1 ($Old): pane $id kept by host $hostPid (version $($k.version), $($k.exe)), shell $shell, running a counter"

  Say "== waiting for the app to install $New and restart itself"
  $seen = @{}
  for ($i = 0; $i -lt 300; $i++) {
    if ((Ver) -eq $New -and -not (Alive $app1)) { break }
    Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -like "$Inst\*" -or $_.CommandLine -clike "* /UPDATE *" } | ForEach-Object {
      if (-not $seen[$_.ProcessId]) {
        $seen[$_.ProcessId] = 1
        Say "  seen at ${i}s: pid $($_.ProcessId) (parent $($_.ParentProcessId)) $($_.CommandLine)"
      }
    }
    Start-Sleep 1
  }
  if ((Ver) -ne $New) { Fail "still $(Ver)" }
  if (Alive $app1) { Fail "the old app did not exit" }
  $app2 = $null
  for ($i = 0; $i -lt 120 -and -not $app2; $i++) { $app2 = App-From-Inst | Select-Object -First 1; if (-not $app2) { Start-Sleep -Milliseconds 500 } }
  if (-not $app2) { Fail "the installer did not start the app again" }
  $app2 = [int]$app2.ProcessId; $script:AppPids += $app2
  Say "installed $(Ver) (suzuri.exe $(Exe-Ver)); app $app1 exited, app $app2 started by the installer"
  for ($i = 0; $i -lt 60 -and (Kept-One $id).viewers -ne 1; $i++) { Start-Sleep -Milliseconds 500 }
  if ((Kept-One $id).viewers -ne 1) { Fail "app $app2 did not attach to shell $id" }
  if (-not (Test-Path (Join-Path $S "local\suzuri\bridge.json"))) { Fail "app $app2 lost the scratch env" }
  Say "app $app2 kept the scratch env (its bridge.json is in $S)"
  Start-Sleep 2
  if (-not (Alive $shell)) { Fail "the shell died" }
  $kk = Kept-One $id
  if ($kk.shell_pid -ne $shell -or $kk.pid -ne $hostPid) { Fail "not the same shell" }
  $snap = (Api-Get "/v1/panes/${id}?lines=500") | ConvertTo-Json -Depth 8 -Compress
  $t = Last-Tick $snap
  $oldest = ([regex]::Match((Screen $id 1000), "scroll-\d+")).Value
  if (-not $t -or $oldest -ne "scroll-1") { Fail "screen or scrollback missing ($t, $oldest)" }
  if ($snap -notmatch "scroll-3\d\d") { Fail "the new app's view of pane $id has no scrollback" }
  Say "app $app2 shows pane ${id}: same host $hostPid and shell $shell, the counter at $t, scrollback back to $oldest"
  Shot $app2 (Join-Path $Out "update-after.png")

  Say "== the keeper update: the open shell keeps its old host, a new pane gets the new one"
  $b = [int](Api-Call "split" @{ pane_id = $id; axis = "right" }).pane_id
  Start-Sleep 3
  $ka = Kept-One $id; $kb = Kept-One $b
  Say "pane ${id}: host $($ka.pid) reports version $($ka.version), program $($ka.exe) (started by the old app)"
  Say "pane ${b}: host $($kb.pid) reports version $($kb.version), program $($kb.exe) (started by the new app)"
  if ($ka.version -ne $Old) { Fail "old host should still be $Old" }
  if ($kb.version -ne $New) { Fail "new host should be $New" }
  if ($ka.exe -eq $kb.exe) { Fail "both hosts run the same program file" }
  Shot $app2 (Join-Path $Out "update-split.png")
  Say "== host programs in the keeper folder"
  Get-ChildItem (Join-Path $KDir "host") | ForEach-Object { Say "  $($_.Name)" }

  $after = (Real-Stamp) -join "; "
  if ($before -ne $after) { Fail "the installed suzuri's config changed: $before -> $after" }
  Say "installed suzuri's config untouched: $after"
  Say "PASS"
} finally {
  Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue
  foreach ($p in (App-From-Inst)) { Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }
  # Uninstall before Cleanup removes the scratch folder the install is in.
  $u = Join-Path $Inst "uninstall.exe"
  if (Test-Path $u) { Start-Process $u -ArgumentList "/S" -Wait; Start-Sleep 3 }
  Cleanup
  # Whatever the uninstaller left of the scratch product (never suzuri's own).
  Remove-Item $UninstKey, "HKCU:\Software\stephenshorton\$Product" -Recurse -ErrorAction SilentlyContinue
  Remove-Item (Join-Path ([Environment]::GetFolderPath("Programs")) "$Product.lnk"), (Join-Path ([Environment]::GetFolderPath("Desktop")) "$Product.lnk") -ErrorAction SilentlyContinue
  Remove-Item (Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "com.stephenshorton.suzuri.keepertest") -Recurse -Force -ErrorAction SilentlyContinue
}
