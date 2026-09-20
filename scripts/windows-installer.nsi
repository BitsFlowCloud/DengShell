Unicode True
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
Name "DengShell"
Caption "DengShell ${VERSION} 安装"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\DengShell"
InstallDirRegKey HKCU "Software\DengShell" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 16
ShowInstDetails show
ShowUninstDetails show
VIProductVersion "${PACKAGE_VERSION}.${BUILD_REVISION}"
VIAddVersionKey /LANG=2052 "ProductName" "DengShell"
VIAddVersionKey /LANG=2052 "FileDescription" "DengShell 当前用户安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "DengShell contributors · MIT"
!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "安装 DengShell"
!define MUI_WELCOMEPAGE_TEXT "将 DengShell 安装到当前用户目录，并创建开始菜单和桌面快捷方式。$\r$\n$\r$\n升级前请先退出 DengShell。原有 data 配置、密钥、字体和背景会保留。$\r$\n$\r$\n应用首次启动会检查 WebView2，缺少时提供安装指引。"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${LICENSE_FILE}"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\DengShell.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 DengShell"
!insertmacro MUI_PAGE_FINISH
!define MUI_UNCONFIRMPAGE_TEXT_TOP "卸载会移除程序和快捷方式，并保留 data 中的个人配置、密钥、字体和背景。"
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Function .onInit
  SetShellVarContext current
  SetRegView 64
  ReadRegStr $0 HKCU "Software\DengShell" "InstallDir"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
  ${EndIf}
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONINFORMATION "此安装包适用于 Windows x64。"
    Abort
  ${EndIf}
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_OK|MB_ICONINFORMATION "DengShell 需要 Windows 10 或更新系统。"
    Abort
  ${EndIf}
FunctionEnd

Section "DengShell" SEC_MAIN
  SetShellVarContext current
  SetRegView 64
  SetOutPath "$INSTDIR"
  SetOverwrite on
  ClearErrors
  File /r "${STAGE}/*"
  IfErrors 0 +3
    MessageBox MB_OK|MB_ICONSTOP "程序文件未能完整写入。请退出 DengShell 后重新运行安装器。"
    Abort
  CreateDirectory "$INSTDIR\data\support"
  WriteINIStr "$INSTDIR\data\support\installed.ini" "DengShell" "Product" "DengShell"
  WriteINIStr "$INSTDIR\data\support\installed.ini" "DengShell" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\data\support\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\DengShell.lnk" "$INSTDIR\DengShell.exe" "" "$INSTDIR\DengShell.exe" 0
  CreateShortcut "$DESKTOP\DengShell.lnk" "$INSTDIR\DengShell.exe" "" "$INSTDIR\DengShell.exe" 0
  WriteRegStr HKCU "Software\DengShell" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "DisplayName" "DengShell"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "Publisher" "DengShell contributors"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "DisplayIcon" "$INSTDIR\DengShell.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "UninstallString" '"$INSTDIR\data\support\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "URLInfoAbout" "https://ds.free-vps.org"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "EstimatedSize" ${ESTIMATED_SIZE}
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell" "NoRepair" 1
SectionEnd

Function un.onInit
  SetShellVarContext current
  SetRegView 64
  ; NSIS relaunches the uninstaller from TEMP and restores the ORIGINAL
  ; uninstaller directory in INSTDIR. EXEDIR can point to that temp copy.
  GetFullPathName $0 "$INSTDIR\..\.."
  ReadINIStr $1 "$0\data\support\installed.ini" "DengShell" "Product"
  ReadINIStr $2 "$0\data\support\installed.ini" "DengShell" "InstallDir"
  ${If} $1 != "DengShell"
    MessageBox MB_OK|MB_ICONEXCLAMATION "无法确认 DengShell 安装目录，已停止卸载。请重新安装到原位置后再卸载。"
    Abort
  ${EndIf}
  ${If} $2 != $0
    MessageBox MB_OK|MB_ICONEXCLAMATION "安装目录与记录不一致，已停止卸载。请重新安装到原位置后再卸载。"
    Abort
  ${EndIf}
  StrCpy $INSTDIR $0
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  SetRegView 64
un_remove_program:
  IfFileExists "$INSTDIR\DengShell.exe" 0 un_program_removed
  ClearErrors
  Delete "$INSTDIR\DengShell.exe"
  IfErrors 0 un_program_removed
  MessageBox MB_RETRYCANCEL|MB_ICONINFORMATION "DengShell 仍在运行或程序文件无法删除。请先退出应用，再重试；取消将保留卸载入口和快捷方式。" IDRETRY un_remove_program
  Abort
un_program_removed:
  ReadRegStr $3 HKCU "Software\DengShell" "InstallDir"
  ${If} $3 == $INSTDIR
    Delete "$SMPROGRAMS\DengShell.lnk"
    Delete "$DESKTOP\DengShell.lnk"
  ${EndIf}
  !include "${UNINSTALL_FILES}"
  Delete "$INSTDIR\data\support\installed.ini"
  Delete "$INSTDIR\data\support\Uninstall.exe"
  RMDir "$INSTDIR\data\support"
  RMDir "$INSTDIR\data"
  RMDir "$INSTDIR"
  ${If} $3 == $INSTDIR
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell"
    DeleteRegKey HKCU "Software\DengShell"
  ${EndIf}
SectionEnd
