param([int]$DengProcessID,[string]$Output)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class DengMouse {
 [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint flags,uint x,uint y,uint data,UIntPtr extra);
}
'@
[DengMouse]::SetProcessDPIAware()|Out-Null
$p=Get-Process -Id $DengProcessID
[DengMouse]::SetForegroundWindow($p.MainWindowHandle)|Out-Null
$root=[System.Windows.Automation.AutomationElement]::FromHandle($p.MainWindowHandle)
if(!$root){throw 'Native main window automation root unavailable'}
$buttonType=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button)
$menuType=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::MenuItem)
$actionType=New-Object System.Windows.Automation.OrCondition($buttonType,$menuType)
function Buttons { @($root.FindAll([System.Windows.Automation.TreeScope]::Descendants,$actionType)) }
function Find-Button([string]$Name){for($n=0;$n -lt 80;$n++){foreach($b in (Buttons)){if($b.Current.Name -eq $Name){return $b}};Start-Sleep -Milliseconds 100};throw "Native button unavailable: $Name"}
function Invoke-Button([string]$Name){
 $b=Find-Button $Name
 if($b.Current.IsOffscreen){
  $scroll=$null
  if($b.TryGetCurrentPattern([System.Windows.Automation.ScrollItemPattern]::Pattern,[ref]$scroll)){$scroll.ScrollIntoView()}else{$b.SetFocus()}
  Start-Sleep -Milliseconds 350
  $b=Find-Button $Name
 }
 $r=$b.Current.BoundingRectangle;$bounds=$root.Current.BoundingRectangle
 if($r.Width -le 0 -or $r.Height -le 0 -or $b.Current.IsOffscreen -or !$bounds.Contains([double]($r.X+$r.Width/2),[double]($r.Y+$r.Height/2))){throw "Button is not visible inside the application: $Name"}
 Write-Host ("Native click: {0}; rectangle={1}; offscreen={2}; desktop={3}" -f $Name,$r,$b.Current.IsOffscreen,[System.Windows.Forms.SystemInformation]::VirtualScreen)
 [DengMouse]::SetCursorPos([int]($r.X+$r.Width/2),[int]($r.Y+$r.Height/2))|Out-Null
 [DengMouse]::mouse_event(2,0,0,0,[UIntPtr]::Zero);[DengMouse]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
 Start-Sleep -Milliseconds 350
}

# Named HTML sections are exposed by the actual WebView2 accessibility tree.
# Reading UIA bounding rectangles avoids introducing a browser debugging port.
function Find-Region([string]$Name){
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$Name)
 for($n=0;$n -lt 80;$n++){
  foreach($element in $root.FindAll([System.Windows.Automation.TreeScope]::Descendants,$condition)){
   $r=$element.Current.BoundingRectangle
   if(!$element.Current.IsOffscreen -and $r.Width -gt 150 -and $r.Height -gt 100){return $element}
  }
  Start-Sleep -Milliseconds 100
 }
 throw "Native region unavailable: $Name"
}
function Bounds-Record($r){@{x=$r.X;y=$r.Y;width=$r.Width;height=$r.Height;right=$r.Right;bottom=$r.Bottom}}
function Require-Layout([bool]$Condition,[string]$Message){if(!$Condition){throw "Native SFTP layout: $Message"}}
function Capture-Native([string]$Name){
 $r=$root.Current.BoundingRectangle;$bitmap=New-Object System.Drawing.Bitmap([int]$r.Width,[int]$r.Height);$g=[System.Drawing.Graphics]::FromImage($bitmap)
 try{$g.CopyFromScreen([int]$r.X,[int]$r.Y,0,0,$bitmap.Size);$bitmap.Save((Join-Path $Output $Name),[System.Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$bitmap.Dispose()}
}
function Test-SFTP-Layout {
 $bottomTerminal=(Find-Region 'SSH 终端').Current.BoundingRectangle
 $bottomFiles=(Find-Region '远程文件').Current.BoundingRectangle
 Require-Layout ($bottomTerminal.Bottom -le $bottomFiles.Y+4 -and [Math]::Abs($bottomTerminal.X-$bottomFiles.X) -lt 4 -and [Math]::Abs($bottomTerminal.Right-$bottomFiles.Right) -lt 4) 'default panels are not stacked'
 Invoke-Button '将 SFTP 放在终端右侧'
 $rightTerminal=(Find-Region 'SSH 终端').Current.BoundingRectangle
 $rightFiles=(Find-Region '远程文件').Current.BoundingRectangle
 Require-Layout ($rightTerminal.Right -le $rightFiles.X+4 -and $rightFiles.X-$rightTerminal.Right -lt 24 -and [Math]::Abs($rightTerminal.Y-$rightFiles.Y) -lt 4 -and [Math]::Abs($rightTerminal.Bottom-$rightFiles.Bottom) -lt 4) 'new position button did not create side-by-side panels'
 Find-Button '将 SFTP 放在终端下方'|Out-Null
 Capture-Native 'windows-native-sftp-right.png'
 Invoke-Button 'SFTP'
 $hiddenTerminal=(Find-Region 'SSH 终端').Current.BoundingRectangle
 Require-Layout ([Math]::Abs($hiddenTerminal.X-$rightTerminal.X) -lt 4 -and [Math]::Abs($hiddenTerminal.Y-$rightTerminal.Y) -lt 4 -and [Math]::Abs($hiddenTerminal.Right-$rightFiles.Right) -lt 4 -and [Math]::Abs($hiddenTerminal.Bottom-$rightFiles.Bottom) -lt 4) 'hidden right panel leaves unused workspace'
 Capture-Native 'windows-native-sftp-hidden.png'
 Invoke-Button 'SFTP'
 $restoredFiles=(Find-Region '远程文件').Current.BoundingRectangle
 Require-Layout ([Math]::Abs($restoredFiles.Width-$rightFiles.Width) -lt 4 -and [Math]::Abs($restoredFiles.X-$rightFiles.X) -lt 4) 'showing the right panel changed its size'
 Invoke-Button '将 SFTP 放在终端下方'
 $restoredTerminal=(Find-Region 'SSH 终端').Current.BoundingRectangle
 $restoredBottom=(Find-Region '远程文件').Current.BoundingRectangle
 Require-Layout ($restoredTerminal.Bottom -le $restoredBottom.Y+4 -and [Math]::Abs($restoredBottom.Height-$bottomFiles.Height) -lt 4 -and [Math]::Abs($restoredTerminal.Right-$bottomTerminal.Right) -lt 4) 'switching back did not restore the remembered bottom layout'
 @{passed=$true;nativeWebView2=$true;method='UI Automation region geometry and real pointer clicks';bottom=@{terminal=(Bounds-Record $bottomTerminal);files=(Bounds-Record $bottomFiles)};right=@{terminal=(Bounds-Record $rightTerminal);files=(Bounds-Record $rightFiles)};hiddenTerminal=(Bounds-Record $hiddenTerminal);restoredRightFiles=(Bounds-Record $restoredFiles);restoredBottomFiles=(Bounds-Record $restoredBottom)}|ConvertTo-Json -Depth 8|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-sftp-layout.json')
}

try{
 Invoke-Button '设置'
 Find-Button '恢复默认布局'|Out-Null
 Invoke-Button '使用手册与功能引导'
 Find-Button '关闭手册'|Out-Null
 Invoke-Button '关闭手册'
 Invoke-Button '设置'
 Invoke-Button '恢复默认布局'
 Test-SFTP-Layout
 Invoke-Button '管理服务器'
 Invoke-Button '折叠全部分组'
 Invoke-Button '1级目录，QA 一级目录，2个连接'
 Find-Button '选择 主连接'|Out-Null
 Find-Button '选择 深层连接'|Out-Null
 Invoke-Button '2级目录，二级目录，1个连接'
 Invoke-Button '3级目录，三级目录，1个连接'
 Invoke-Button '选择 深层连接'
 Find-Button '打开所选 (1)'|Out-Null
 Invoke-Button '管理连接 深层连接'
 foreach($name in @('连接','编辑','删除','定位所属分组','复制地址')){Find-Button $name|Out-Null}
 # Capture the actual visible native window; no browser debugging is enabled.
 $r=$root.Current.BoundingRectangle
 $bitmap=New-Object System.Drawing.Bitmap([int]$r.Width,[int]$r.Height)
 $g=[System.Drawing.Graphics]::FromImage($bitmap)
 try{$g.CopyFromScreen([int]$r.X,[int]$r.Y,0,0,$bitmap.Size);$bitmap.Save((Join-Path $Output 'windows-native-manager.png'),[System.Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$bitmap.Dispose()}
 @{passed=$true;nativeWebView2=$true;method='Windows UI Automation on the exact packed release';directorySelection=$true;nameClickExpandsCollapsedGroup=$true;deepFolder=$true;singleClickSelection=$true;contextMenu=$true;leftSettingsMenu=$true;manualFromSettings=$true;restoreLayoutFromSettings=$true;sftpRightLayout=$true;sftpHiddenFillsWorkspace=$true;sftpRememberedSizes=$true}|ConvertTo-Json|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-validation.json')
 Write-Host 'PASS: native Windows SFTP right/hidden/restored geometry, directory navigation and context menu.'
} catch {
 (Buttons)|ForEach-Object {$_.Current.Name}|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-controls.txt')
 (Buttons)|ForEach-Object {@{name=$_.Current.Name;rect=$_.Current.BoundingRectangle.ToString();offscreen=$_.Current.IsOffscreen}}|ConvertTo-Json|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-geometry.json')
 $r=[System.Windows.Forms.SystemInformation]::VirtualScreen;$bitmap=New-Object System.Drawing.Bitmap($r.Width,$r.Height);$g=[System.Drawing.Graphics]::FromImage($bitmap)
 try{$g.CopyFromScreen($r.X,$r.Y,0,0,$bitmap.Size);$bitmap.Save((Join-Path $Output 'windows-failure-desktop.png'),[System.Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$bitmap.Dispose()}
 throw
}
