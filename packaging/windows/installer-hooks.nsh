; Tauri NSIS installer hooks (bundle.windows.nsis.installerHooks).
;
; Tauri's per-user default install folder is $LOCALAPPDATA\${PRODUCTNAME}.
; For "suzuri" that is the config folder this app shares with Go suzuri
; (config.json, bridge.json, ai\, workspace, notes), so binaries would mix
; into it. Install beside Go suzuri instead. Not $LOCALAPPDATA\Programs\suzuri:
; that is Go suzuri's install, whose suzuri.exe (suzuri mcp) and
; suzuri-transfer.exe this app still runs. A folder from the registry (an
; update) or picked by the user is kept as it is.
!macro NSIS_HOOK_PREINSTALL
  ${If} $INSTDIR == "$LOCALAPPDATA\${PRODUCTNAME}"
    StrCpy $INSTDIR "$LOCALAPPDATA\Programs\suzuri-app"
    SetOutPath $INSTDIR
  ${EndIf}
!macroend
