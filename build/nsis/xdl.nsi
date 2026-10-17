; XDL per-user NSIS installer (Plan §4.10)
; Build after scripts\build-alpha.ps1 has produced %LOCALAPPDATA%\XDL-dev\xdl.exe
;   makensis /DVERSION=0.0.5-m5 /DOUTDIR=...\dist build\nsis\xdl.nsi

!ifndef VERSION
  !define VERSION "0.0.5-m5"
!endif
!ifndef STAGE_DIR
  !define STAGE_DIR "$%LOCALAPPDATA%\XDL-dev"
!endif
!ifndef OUTDIR
  !define OUTDIR "."
!endif

Name "XDL"
OutFile "${OUTDIR}\XDL-${VERSION}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\XDL"
RequestExecutionLevel user
Unicode true
SetCompressor /SOLID lzma

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  File "${STAGE_DIR}\xdl.exe"
  IfFileExists "${STAGE_DIR}\extension\manifest.json" 0 +3
    CreateDirectory "$INSTDIR\extension"
    CopyFiles /SILENT "${STAGE_DIR}\extension\*.*" "$INSTDIR\extension"

  ; Register native messaging host via the installed binary.
  ExecWait '"$INSTDIR\xdl.exe" --install-native-host'

  CreateDirectory "$SMPROGRAMS\XDL"
  CreateShortCut "$SMPROGRAMS\XDL\XDL.lnk" "$INSTDIR\xdl.exe"
  CreateShortCut "$SMPROGRAMS\XDL\卸载 XDL.lnk" "$INSTDIR\Uninstall.exe"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL" "DisplayName" "XDL"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL" "Publisher" "XDL"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL" "UninstallString" '"$INSTDIR\Uninstall.exe"'
SectionEnd

Section "Uninstall"
  ExecWait '"$INSTDIR\xdl.exe" --uninstall-native-host'
  Delete "$INSTDIR\xdl.exe"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir /r "$INSTDIR\extension"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\XDL\XDL.lnk"
  Delete "$SMPROGRAMS\XDL\卸载 XDL.lnk"
  RMDir "$SMPROGRAMS\XDL"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL"
  ; Data under %APPDATA%\XDL is left for the user to keep download history.
SectionEnd
