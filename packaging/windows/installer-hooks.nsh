; Tauri NSIS installer hooks (bundle.windows.nsis.installerHooks).
;
; This installer replaces Go suzuri (0.9.x), which installed per user to
; $LOCALAPPDATA\Programs\suzuri with its own NSIS script
; (go-legacy: packaging/windows/suzuri.nsi). Both use the same Add/Remove
; Programs key, Uninstall\suzuri, which Tauri reads as a previous install of
; itself.
;
; Never touched here: $LOCALAPPDATA\suzuri, the config folder Go and Rust
; share (config.json, bridge.json, ai\, notes).
;
; Tauri !includes this file before its pages and .onInit, so only our own
; Vars can be used here (Tauri's are declared after the include).

!define SUZURI_UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri"
!define SUZURI_GOKEY "Software\suzuri"

Var SuzuriGoUninst ; Go's UninstallString, set aside while the installer runs
Var SuzuriGoDir    ; Go's install folder, when Go suzuri is installed
Var SuzuriTmp

; 1. Keep Tauri's reinstall page from running Go's Uninstall.exe.
;
; On an upgrade the page offers "uninstall before installing" (checked by
; default, and taken in passive mode) and runs the key's UninstallString with
; "_?=<Tauri's own install key>", which is empty for Go. Go's uninstaller
; would then taskkill every suzuri.exe and delete suzuri.exe and the key,
; possibly racing the files this installer writes. Go's install is a Go
; install when the key has an UninstallString but no MainBinaryName (Tauri
; writes MainBinaryName on every install). Moving UninstallString aside makes
; the page skip itself; it comes back if the install is cancelled or fails.
; Silent installs (/S) show no pages, so this is not needed there.
!define MUI_CUSTOMFUNCTION_GUIINIT SuzuriHideGoUninstaller
!define MUI_CUSTOMFUNCTION_ABORT SuzuriRestoreGoUninstaller

Function SuzuriHideGoUninstaller
  ReadRegStr $SuzuriTmp HKCU "${SUZURI_UNINSTKEY}" "MainBinaryName"
  ${If} $SuzuriTmp == ""
    ReadRegStr $SuzuriGoUninst HKCU "${SUZURI_UNINSTKEY}" "UninstallString"
    ${If} $SuzuriGoUninst != ""
      WriteRegStr HKCU "${SUZURI_UNINSTKEY}" "GoUninstallString" $SuzuriGoUninst
      DeleteRegValue HKCU "${SUZURI_UNINSTKEY}" "UninstallString"
    ${EndIf}
  ${EndIf}
FunctionEnd

Function SuzuriRestoreGoUninstaller
  ${If} $SuzuriGoUninst != ""
    ReadRegStr $SuzuriTmp HKCU "${SUZURI_UNINSTKEY}" "MainBinaryName"
    ${If} $SuzuriTmp == ""
      WriteRegStr HKCU "${SUZURI_UNINSTKEY}" "UninstallString" $SuzuriGoUninst
      DeleteRegValue HKCU "${SUZURI_UNINSTKEY}" "GoUninstallString"
    ${EndIf}
  ${EndIf}
FunctionEnd

Function .onInstFailed
  Call SuzuriRestoreGoUninstaller
FunctionEnd

; 2. Install where Go suzuri is.
;
; Tauri's per-user default is $LOCALAPPDATA\${PRODUCTNAME}, which for
; "suzuri" is the config folder. Use Go's folder instead (its InstallDir, or
; Programs\suzuri when Go isn't installed). An update keeps the folder from
; Tauri's own registry key, and a folder the user picks is kept too. This runs
; before Tauri's running-app check, so a running suzuri.exe in that folder
; (Go's) is closed the way Tauri closes its own app.
!macro NSIS_HOOK_PREINSTALL
  ReadRegStr $SuzuriGoDir HKCU "${SUZURI_GOKEY}" "InstallDir"
  ${If} $SuzuriGoDir == ""
  ${AndIf} $SuzuriGoUninst != ""
    StrCpy $SuzuriGoDir "$LOCALAPPDATA\Programs\suzuri"
  ${EndIf}
  ${If} $INSTDIR == "$LOCALAPPDATA\${PRODUCTNAME}"
    ${If} $SuzuriGoDir != ""
    ${AndIf} $SuzuriGoDir != "$LOCALAPPDATA\suzuri"
      StrCpy $INSTDIR $SuzuriGoDir
    ${Else}
      StrCpy $INSTDIR "$LOCALAPPDATA\Programs\suzuri"
    ${EndIf}
    SetOutPath $INSTDIR
  ${EndIf}
!macroend

; 3. After the copy: remove what Go's uninstaller would have, except what
; this app still uses.
;
; Go's Uninstall.exe deletes suzuri.exe(.old), suzuri-transfer.exe(.old),
; suzuri-chrome.exe(.old), suzuri.ico and Uninstall.exe in its folder, its
; Start menu folder and desktop shortcut, Uninstall\suzuri and
; Software\suzuri, and leaves $LOCALAPPDATA\suzuri. Here suzuri.exe has
; just been replaced, Uninstall.exe is Tauri's uninstall.exe now (same name
; on NTFS), the key has just been rewritten by Tauri, and suzuri-transfer.exe
; stays: this app runs it (and ships its own over it when bundled).
!macro NSIS_HOOK_POSTINSTALL
  DeleteRegValue HKCU "${SUZURI_UNINSTKEY}" "GoUninstallString"
  ${If} $SuzuriGoDir != ""
    nsExec::ExecToLog 'taskkill /IM suzuri-chrome.exe /F'
    Pop $SuzuriTmp
    Delete "$INSTDIR\suzuri.exe.old"
    Delete "$INSTDIR\suzuri-transfer.exe.old"
    Delete "$INSTDIR\suzuri-chrome.exe"
    Delete "$INSTDIR\suzuri-chrome.exe.old"
    Delete "$INSTDIR\suzuri.ico"
    Delete "$SMPROGRAMS\suzuri\suzuri.lnk"
    Delete "$SMPROGRAMS\suzuri\Uninstall suzuri.lnk"
    RMDir "$SMPROGRAMS\suzuri"
    ; Go's desktop shortcut has the same name as Tauri's; point it at the
    ; new exe's own icon, since suzuri.ico is gone.
    ${If} ${FileExists} "$DESKTOP\suzuri.lnk"
      CreateShortCut "$DESKTOP\suzuri.lnk" "$INSTDIR\suzuri.exe"
    ${EndIf}
    ; Go wrote this one; Tauri doesn't, so it would outlive Go.
    DeleteRegValue HKCU "${SUZURI_UNINSTKEY}" "QuietUninstallString"
    DeleteRegKey HKCU "${SUZURI_GOKEY}"
  ${EndIf}
!macroend
