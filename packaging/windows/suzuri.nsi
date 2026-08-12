; suzuri Windows installer (NSIS)
; User-scoped: no admin, installs under %LOCALAPPDATA%\Programs\suzuri
; Auto-update can still replace the exe in place (directory is writable).
;
; Build (from repo root after go build):
;   makensis /DVERSION=0.9.2 /DOUTFILE=suzuri-0.9.2-windows-amd64-setup.exe \
;            /DSOURCE_EXE=suzuri.exe packaging\windows\suzuri.nsi

Unicode true
CRCCheck on
SetCompressor /SOLID lzma

!ifndef VERSION
  !define VERSION "0.0.0-dev"
!endif
!ifndef OUTFILE
  !define OUTFILE "suzuri-setup.exe"
!endif
!ifndef SOURCE_EXE
  !define SOURCE_EXE "suzuri.exe"
!endif
!ifndef SOURCE_TRANSFER
  !define SOURCE_TRANSFER ""
!endif
!ifndef SOURCE_CHROME
  !define SOURCE_CHROME ""
!endif
!ifndef ICON
  !define ICON "assets\icon\suzuri.ico"
!endif

Name "suzuri ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\suzuri"
InstallDirRegKey HKCU "Software\suzuri" "InstallDir"
RequestExecutionLevel user
ShowInstDetails show
ShowUnInstDetails show

; Modern UI
!include "MUI2.nsh"
!include "FileFunc.nsh"

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"

!define MUI_WELCOMEPAGE_TITLE "Install suzuri ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "suzuri (硯) is a native terminal host.$\r$\n$\r$\nThis installs for your user only (no administrator rights).$\r$\n$\r$\nLocation: %LOCALAPPDATA%\Programs\suzuri$\r$\nConfig / logs stay in %LOCALAPPDATA%\suzuri\"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\suzuri.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Launch suzuri"
!define MUI_FINISHPAGE_LINK "suzuri on GitHub"
!define MUI_FINISHPAGE_LINK_LOCATION "https://github.com/StephenSHorton/suzuri"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=1033 "ProductName" "suzuri"
VIAddVersionKey /LANG=1033 "FileDescription" "suzuri terminal host installer"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "MIT © Stephen S. Horton"
VIAddVersionKey /LANG=1033 "CompanyName" "Stephen S. Horton"

Section "suzuri" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"

  ; Stop a running instance so we can overwrite the exe.
  nsExec::ExecToLog 'taskkill /IM suzuri.exe /F'
  Pop $0
  nsExec::ExecToLog 'taskkill /IM suzuri-transfer.exe /F'
  Pop $0
  nsExec::ExecToLog 'taskkill /IM suzuri-chrome.exe /F'
  Pop $0
  Sleep 300

  File "/oname=suzuri.exe" "${SOURCE_EXE}"
  !if "${SOURCE_TRANSFER}" != ""
    File "/oname=suzuri-transfer.exe" "${SOURCE_TRANSFER}"
  !endif
  !if "${SOURCE_CHROME}" != ""
    File "/oname=suzuri-chrome.exe" "${SOURCE_CHROME}"
  !endif
  File "${ICON}"

  ; Start Menu
  CreateDirectory "$SMPROGRAMS\suzuri"
  CreateShortCut "$SMPROGRAMS\suzuri\suzuri.lnk" "$INSTDIR\suzuri.exe" "" "$INSTDIR\suzuri.ico" 0
  CreateShortCut "$SMPROGRAMS\suzuri\Uninstall suzuri.lnk" "$INSTDIR\Uninstall.exe"

  ; Optional desktop shortcut
  CreateShortCut "$DESKTOP\suzuri.lnk" "$INSTDIR\suzuri.exe" "" "$INSTDIR\suzuri.ico" 0

  ; Remember install dir
  WriteRegStr HKCU "Software\suzuri" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\suzuri" "Version" "${VERSION}"

  ; Add/Remove Programs (per-user)
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "DisplayName" "suzuri ${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "Publisher" "Stephen S. Horton"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "DisplayIcon" "$INSTDIR\suzuri.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "URLInfoAbout" "https://github.com/StephenSHorton/suzuri"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "NoRepair" 1

  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri" \
    "EstimatedSize" "$0"
SectionEnd

Section "Uninstall"
  nsExec::ExecToLog 'taskkill /IM suzuri.exe /F'
  Pop $0
  nsExec::ExecToLog 'taskkill /IM suzuri-transfer.exe /F'
  Pop $0
  nsExec::ExecToLog 'taskkill /IM suzuri-chrome.exe /F'
  Pop $0
  Sleep 300

  Delete "$INSTDIR\suzuri.exe"
  Delete "$INSTDIR\suzuri.exe.old"
  Delete "$INSTDIR\suzuri-transfer.exe"
  Delete "$INSTDIR\suzuri-transfer.exe.old"
  Delete "$INSTDIR\suzuri-chrome.exe"
  Delete "$INSTDIR\suzuri-chrome.exe.old"
  Delete "$INSTDIR\suzuri.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"

  Delete "$SMPROGRAMS\suzuri\suzuri.lnk"
  Delete "$SMPROGRAMS\suzuri\Uninstall suzuri.lnk"
  RMDir "$SMPROGRAMS\suzuri"
  Delete "$DESKTOP\suzuri.lnk"

  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\suzuri"
  DeleteRegKey HKCU "Software\suzuri"

  ; Leave config/logs in %LOCALAPPDATA%\suzuri so reinstall keeps settings.
SectionEnd
