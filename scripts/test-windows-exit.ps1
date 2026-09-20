param([Parameter(Mandatory=$true)][string]$Exe,[Parameter(Mandatory=$true)][string]$Output)
$ErrorActionPreference='Stop'
$Exe=(Resolve-Path $Exe).Path
New-Item -ItemType Directory -Force $Output | Out-Null
$Output=(Resolve-Path $Output).Path
$exitRoot=Join-Path $env:RUNNER_TEMP ('DengShell exit '+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $exitRoot | Out-Null
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class DengExitQA {
 public delegate bool EnumProc(IntPtr h,IntPtr l);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f,IntPtr l);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h,StringBuilder s,int n);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h,int n);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint f,uint x,uint y,uint d,UIntPtr e);
 [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
 public static IntPtr Find(uint pid,string cls){IntPtr found=IntPtr.Zero;EnumWindows((h,l)=>{uint p;GetWindowThreadProcessId(h,out p);var s=new StringBuilder(256);GetClassName(h,s,256);if(p==pid && s.ToString()==cls){found=h;return false;}return true;},IntPtr.Zero);return found;}
}
'@
[DengExitQA]::SetProcessDPIAware()|Out-Null
$report=[ordered]@{passed=$false;checks=@();executableSHA256=(Get-FileHash $Exe).Hash.ToLowerInvariant()}
$processes=@()
function Wait-ExitQA([scriptblock]$Condition,[string]$Label,[int]$Seconds=45){$until=[DateTime]::UtcNow.AddSeconds($Seconds);do{if(& $Condition){return};Start-Sleep -Milliseconds 100}while([DateTime]::UtcNow -lt $until);throw "Timed out: $Label"}
function Control([string]$Name){
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$Name)
 foreach($item in $script:root.FindAll([System.Windows.Automation.TreeScope]::Descendants,$condition)){if(!$item.Current.IsOffscreen -and $item.Current.BoundingRectangle.Width -gt 0){return $item}}
 return $null
}
function Click([string]$Name){Wait-ExitQA {$null -ne (Control $Name)} $Name;$item=Control $Name;$r=$item.Current.BoundingRectangle;[DengExitQA]::SetCursorPos([int]($r.X+$r.Width/2),[int]($r.Y+$r.Height/2))|Out-Null;[DengExitQA]::mouse_event(2,0,0,0,[UIntPtr]::Zero);[DengExitQA]::mouse_event(4,0,0,0,[UIntPtr]::Zero);Start-Sleep -Milliseconds 200}
function Passed([string]$Label){$script:report.checks+=$Label;Write-Host "PASS: $Label"}
function Start-ExitNative([string]$Config){
 $p=Start-Process $Exe -ArgumentList @('--config',('"'+$Config+'"')) -PassThru
 $script:processes+=$p
 Wait-ExitQA {(Test-Path (Join-Path $Config 'runtime-success-v1-windows-amd64')) -and [DengExitQA]::Find($p.Id,'DengShellWindow') -ne [IntPtr]::Zero} 'native frontend ready'
 $script:handle=[DengExitQA]::Find($p.Id,'DengShellWindow');[DengExitQA]::SetForegroundWindow($script:handle)|Out-Null
 $script:root=[System.Windows.Automation.AutomationElement]::FromHandle($script:handle)
 return $p
}
function Confirm-Persistent(){Wait-ExitQA {$null -ne (Control '继续使用')} 'unlocked confirmation';Start-Sleep -Milliseconds 1000;if($null -eq (Control '继续使用')){throw 'Exit confirmation disappeared after focus/lock status refresh'}}
try{
 $config=Join-Path $exitRoot 'locked config';New-Item -ItemType Directory $config|Out-Null
 $seed=@{schemaVersion=2;appearance=@{onboardingCompleted=$true;startupAnimation=$false;minimizeAction='tray'}}
 [IO.File]::WriteAllText((Join-Path $config 'config.json'),($seed|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 $errorLog=Join-Path $exitRoot 'fixture-private.log'
 $fixture=Start-Process $Exe -ArgumentList @('--browser','--config',('"'+$config+'"')) -PassThru -RedirectStandardError $errorLog -RedirectStandardOutput (Join-Path $exitRoot 'fixture-out.log');$processes+=$fixture
 Wait-ExitQA {(Test-Path $errorLog) -and (Get-Content $errorLog -Raw) -match 'http://127\.0\.0\.1:\d+/#token=\S+'} 'isolated fixture API'
 $match=[regex]::Match((Get-Content $errorLog -Raw),'(http://127\.0\.0\.1:\d+)/#token=(\S+)');$base=$match.Groups[1].Value;$headers=@{'X-CloudShell-Token'=$match.Groups[2].Value}
 $authorization=Invoke-RestMethod ($base+'/api/security-lock/authorize') -Method Post -Headers $headers -ContentType 'application/json' -Body '{}'
 Invoke-RestMethod ($base+'/api/security-lock/settings') -Method Post -Headers $headers -ContentType 'application/json' -Body (@{grant=$authorization.grant;enabled=$true;passwordEnabled=$true;password='4826';idleSeconds=0}|ConvertTo-Json)|Out-Null
 Stop-Process -Id $fixture.Id -Force;$fixture.WaitForExit();Start-Sleep -Milliseconds 300
 $running=Start-ExitNative $config
 Click '解锁'
 Wait-ExitQA {$null -ne (Control '解锁密码')} 'password field'
 $field=Control '解锁密码';$field.SetFocus();[System.Windows.Forms.SendKeys]::SendWait('4826');Click '解锁'
 Wait-ExitQA {$null -ne (Control '设置')} 'unlocked workspace'
 Click '关闭窗口';Confirm-Persistent
 [DengExitQA]::PostMessage($handle,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null;Confirm-Persistent
 Click '继续使用';Passed 'Titlebar close remains visible with security enabled; repeated close and cancellation work'
 [DengExitQA]::ShowWindow($handle,6)|Out-Null;Start-Sleep -Milliseconds 600
 [DengExitQA]::PostMessage($handle,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null;Confirm-Persistent;Click '继续使用'
 Passed 'Taskbar-equivalent WM_CLOSE restores hidden/minimized window and keeps confirmation visible'
 $tray=[DengExitQA]::Find($running.Id,'DengShellTrayWindow');if($tray -eq [IntPtr]::Zero){throw 'Native tray window missing'}
 [DengExitQA]::PostMessage($tray,0x8001,[IntPtr]::Zero,[IntPtr]0x007b)|Out-Null
 Start-Sleep -Milliseconds 400
 [System.Windows.Forms.SendKeys]::SendWait('{END}{ENTER}')
 Confirm-Persistent;Click '继续使用';Passed 'Actual native tray context-menu exit reaches persistent confirmation'
 Click '立即锁定';Wait-ExitQA {$null -ne (Control '软件已被锁定')} 'locked screen'
 Click '解锁';Wait-ExitQA {$null -ne (Control '返回锁定界面')} 'unlock form'
 [DengExitQA]::PostMessage($handle,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
 Click '继续保持锁定';Wait-ExitQA {$null -ne (Control '返回锁定界面')} 'unlock form restored after cancellation'
 [DengExitQA]::PostMessage($handle,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
 Wait-ExitQA {$null -ne (Control '继续保持锁定')} 'locked exit confirmation'
 $confirm=$root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,(New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty,'security-exit-confirm')))
 if($null -ne $confirm){$invoke=$confirm.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern);$invoke.Invoke()}else{Click '继续保持锁定';[DengExitQA]::PostMessage($handle,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null;Wait-ExitQA {$null -ne (Control '继续保持锁定')} 'locked confirmation';[System.Windows.Forms.SendKeys]::SendWait('+{TAB}{ENTER}')}
 Wait-ExitQA {$running.Refresh();$running.HasExited} 'locked native process exits'
 Passed 'Password-unlock screen can cancel and then confirm native exit without unlocking'
 $plain=Join-Path $exitRoot 'plain config';New-Item -ItemType Directory $plain|Out-Null
 [IO.File]::WriteAllText((Join-Path $plain 'config.json'),($seed|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 $running=Start-ExitNative $plain;Click '关闭窗口';Confirm-Persistent;Click '退出';Wait-ExitQA {$running.Refresh();$running.HasExited} 'normal native process exits';Passed 'Security disabled normal confirmed exit terminates process'
 $report.passed=$true
}finally{
 foreach($p in $processes){Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue}
 $report|ConvertTo-Json -Depth 8|Set-Content -Encoding UTF8 (Join-Path $Output 'windows-exit-validation.json')
}
