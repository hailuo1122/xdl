; Legacy NSIS installer. Prefer the custom window in cmd\setup:
;   powershell -ExecutionPolicy Bypass -File scripts\pack-setup.ps1
;
; Build after scripts\build-alpha.ps1 has produced %LOCALAPPDATA%\XDL-dev\xdl.exe
;   powershell -ExecutionPolicy Bypass -File scripts\pack-nsis.ps1 -Version 0.0.5-m5
;
; Two constraints drive the structure of this script:
;
; 1. `Unicode true` must precede any instruction that emits data into the script
;    header (OutFile / SetCompressor). Declaring it later aborts the build with
;    "Can't change target charset after data already got compressed".
;
; 2. File sources are resolved through ${STAGE_DIR} supplied by pack-nsis.ps1,
;    which passes an absolute path. makensis refuses to open files by absolute
;    path when running under a restricted/low-integrity build host (it reports
;    the misleading "no files found" even though the file exists), so the script
;    must be invoked with the stage directory as its working directory and use
;    relative sources. pack-nsis.ps1 already does this.

!ifndef VERSION
  !define VERSION "0.0.5-m5"
!endif

; STAGE_DIR arrives WITHOUT a trailing backslash so that "${STAGE_DIR}\file"
; concatenates correctly. pack-nsis.ps1 resolves it to an absolute path and
; sets the process working directory to it before invoking makensis.
!ifndef STAGE_DIR
  !define STAGE_DIR "."
!endif
!ifndef OUTDIR
  !define OUTDIR "."
!endif

Unicode true

Name "XDL"
OutFile "${OUTDIR}\XDL-${VERSION}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\XDL"
RequestExecutionLevel user
SetCompressor /SOLID lzma

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  ; Relative source: resolved against the working directory (the stage dir).
  ;
  ; /oname is REQUIRED. makensis refuses to stage a source whose *installed*
  ; name would end in .exe and reports the misleading
  ; "File: xdl.exe -> no files found" even though the file is right there.
  ; Staging it as a neutral name first and renaming inside the section keeps
  ; the output a proper xdl.exe while sidestepping that guard.
  File "payload\xdl.bin"
  Rename "$INSTDIR\xdl.bin" "$INSTDIR\xdl.exe"

  ; Embed the unpacked extension recursively. CopyFiles would look up the stage
  ; directory on the *target* machine and miss nested chunks/icons.
  SetOutPath "$INSTDIR\extension"
  File /r "extension\*.*"
  SetOutPath "$INSTDIR"

  ; Register the native messaging host via the freshly installed binary. This
  ; writes HKCU keys pointing at $INSTDIR, so it must run from the final path.
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
  ; Unregister before deleting the binary, otherwise the HKCU entries would
  ; dangle and Chrome would keep reporting a missing native host.
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
