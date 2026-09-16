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

try{
 Invoke-Button '设置'
 Find-Button '恢复默认布局'|Out-Null
 Invoke-Button '使用手册与功能引导'
 Find-Button '关闭手册'|Out-Null
 Invoke-Button '关闭手册'
 Invoke-Button '设置'
 Invoke-Button '恢复默认布局'
 Invoke-Button '管理服务器'
 Invoke-Button '1级目录，QA 一级目录，2个连接'
 Find-Button '选择 主连接'|Out-Null
 Find-Button '选择 深层连接'|Out-Null
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
 @{passed=$true;nativeWebView2=$true;method='Windows UI Automation on the exact packed release';directorySelection=$true;deepFolder=$true;singleClickSelection=$true;contextMenu=$true;leftSettingsMenu=$true;manualFromSettings=$true;restoreLayoutFromSettings=$true}|ConvertTo-Json|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-validation.json')
 Write-Host 'PASS: native Windows directory navigation, single-click selection and context menu.'
} catch {
 (Buttons)|ForEach-Object {$_.Current.Name}|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-controls.txt')
 (Buttons)|ForEach-Object {@{name=$_.Current.Name;rect=$_.Current.BoundingRectangle.ToString();offscreen=$_.Current.IsOffscreen}}|ConvertTo-Json|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-ui-geometry.json')
 $r=[System.Windows.Forms.SystemInformation]::VirtualScreen;$bitmap=New-Object System.Drawing.Bitmap($r.Width,$r.Height);$g=[System.Drawing.Graphics]::FromImage($bitmap)
 try{$g.CopyFromScreen($r.X,$r.Y,0,0,$bitmap.Size);$bitmap.Save((Join-Path $Output 'windows-failure-desktop.png'),[System.Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$bitmap.Dispose()}
 throw
}
