param([Parameter(Mandatory=$true)][string]$Artifacts,[Parameter(Mandatory=$true)][string]$LegacyZip,[string]$Output='qa-results',[string]$LegacyRelease='legacy')
$ErrorActionPreference='Stop'
$Artifacts=(Resolve-Path $Artifacts).Path; $LegacyZip=(Resolve-Path $LegacyZip).Path
New-Item -ItemType Directory -Force $Output | Out-Null; $Output=(Resolve-Path $Output).Path
$qaRoot=Join-Path $env:RUNNER_TEMP ('DengShell QA '+[guid]::NewGuid().ToString('N')); New-Item -ItemType Directory $qaRoot | Out-Null
$report=[ordered]@{os=(Get-CimInstance Win32_OperatingSystem).Caption;architecture=$env:PROCESSOR_ARCHITECTURE;checks=@();legacyRelease=$LegacyRelease;passed=$false}
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class DengQA {
 public delegate bool EnumProc(IntPtr h,IntPtr l);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f,IntPtr l);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h,StringBuilder s,int n);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h,StringBuilder s,int n);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h,int command);
 public static IntPtr Find(uint pid) { IntPtr found=IntPtr.Zero;EnumWindows((h,l)=>{uint p;GetWindowThreadProcessId(h,out p);var s=new StringBuilder(256);GetClassName(h,s,256);if(p==pid && s.ToString()=="DengShellWindow"){found=h;return false;}return true;},IntPtr.Zero);return found; }
}
'@
function Wait-QA([scriptblock]$Condition,[string]$Name,[int]$Seconds=60){$deadline=[DateTime]::UtcNow.AddSeconds($Seconds);do{if(& $Condition){return};Start-Sleep -Milliseconds 250}while([DateTime]::UtcNow -lt $deadline);throw "Timed out: $Name"}
function Check-QA([bool]$Condition,[string]$Name){if(!$Condition){throw "Failed: $Name"};$script:report.checks+= $Name;Write-Host "PASS: $Name"}
function Stop-QA($Process){if($Process -and !(Get-Process -Id $Process.Id -ErrorAction SilentlyContinue).HasExited){Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue;Start-Sleep -Milliseconds 700}}
function Seed-QA([string]$Config){
 New-Item -ItemType Directory -Force $Config | Out-Null
 $fixture=@{schemaVersion=2;groups=@('QA 一级目录');groupNodes=@(@{id='qa-root';name='QA 一级目录';parentId=''},@{id='qa-child';name='二级目录';parentId='qa-root'},@{id='qa-deep';name='三级目录';parentId='qa-child'});servers=@(@{id='qa-main';name='主连接';host='192.0.2.10';port=22;user='root';auth='password';secret='local-qa-only';groupId='qa-root'},@{id='qa-nested';name='深层连接';host='192.0.2.11';port=22;user='root';auth='password';secret='local-qa-only';groupId='qa-deep'});appearance=@{minimizeAction='tray';onboardingCompleted=$true;startupAnimation=$false}}
 [IO.File]::WriteAllText((Join-Path $Config 'config.json'),($fixture|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false));Set-Content -Encoding utf8 (Join-Path $Config 'keep-user-data.txt') 'preserve-local-fixture'
}
function Start-Native([string]$Exe,[string]$Config,[bool]$Hidden=$false){$marker=Join-Path $Config 'runtime-success-v1-windows-amd64';Remove-Item $marker -Force -ErrorAction SilentlyContinue;$opts=@{FilePath=$Exe;ArgumentList=@('--config',('"'+$Config+'"'));PassThru=$true;WorkingDirectory=(Split-Path $Exe)};if($Hidden){$opts.WindowStyle='Hidden'};$p=Start-Process @opts;Wait-QA {Test-Path $marker} 'native frontend ready';Wait-QA {[DengQA]::Find($p.Id) -ne [IntPtr]::Zero} 'main window';return $p}
function Visible-QA($Process,[string]$Name){Wait-QA {$h=[DengQA]::Find($Process.Id);$h -ne [IntPtr]::Zero -and [DengQA]::IsWindowVisible($h) -and ![DengQA]::IsIconic($h)} $Name;Check-QA $true $Name}
try{
 $portable=Join-Path $qaRoot 'portable';Expand-Archive (Join-Path $Artifacts 'DengShell-windows-x64.zip') $portable
 $exe=(Get-ChildItem $portable -Filter DengShell.exe -Recurse | Select-Object -First 1).FullName
 $expected=(Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant();$report.executableSHA256=$expected
 $report.installerSHA256=(Get-FileHash (Join-Path $Artifacts 'DengShell-Setup-x64.exe')).Hash.ToLowerInvariant()
 $config=Join-Path $qaRoot 'portable config';Seed-QA $config;$running=Start-Native $exe $config $true;Visible-QA $running 'packed portable EXE launches visibly even with SW_HIDE';powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-windows-ui.ps1 -DengProcessID $running.Id -Output $Output;if($LASTEXITCODE -ne 0){throw 'Native Windows UI Automation check failed'};Stop-QA $running
 $install=Join-Path $qaRoot 'installed';$installer=Start-Process (Join-Path $Artifacts 'DengShell-Setup-x64.exe') -ArgumentList ('/S /D='+$install) -PassThru -Wait;Check-QA ($installer.ExitCode -eq 0) 'silent installer returns success'
 Check-QA ((Get-FileHash (Join-Path $install 'DengShell.exe')).Hash.ToLowerInvariant() -eq $expected) 'installer contains exact portable executable'
 $uninstallKey='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\DengShell';Check-QA ((Get-ItemProperty $uninstallKey).DisplayName -eq 'DengShell') 'registered in Windows uninstall control panel'
 $installedConfig=Join-Path $install 'data';Seed-QA $installedConfig;$running=Start-Native (Join-Path $install 'DengShell.exe') $installedConfig;Visible-QA $running 'installed native WebView2 frontend starts';Stop-QA $running
 $uninstaller=Start-Process (Join-Path $install 'data/support/Uninstall.exe') -ArgumentList '/S' -PassThru -Wait;Wait-QA {!(Test-Path (Join-Path $install 'DengShell.exe'))} 'uninstall removes executable';Check-QA (!(Test-Path $uninstallKey)) 'uninstall registration removed';Check-QA ((Get-Content (Join-Path $installedConfig 'keep-user-data.txt')).Trim() -eq 'preserve-local-fixture') 'uninstall preserves user data'
 $legacy=Join-Path $qaRoot 'legacy';Expand-Archive $LegacyZip $legacy;$oldExe=(Get-ChildItem $legacy -Filter DengShell.exe -Recurse|Select-Object -First 1).FullName
 $build=[uint64]([regex]::Match((Get-Content internal/app/updates.go -Raw),'const ApplicationBuild uint64 = (\d+)').Groups[1].Value)
 foreach($mode in @('legacy','current')){
  $case=Join-Path $qaRoot ('upgrade-'+$mode);New-Item -ItemType Directory $case | Out-Null;$target=Join-Path $case 'DengShell.exe';Copy-Item $(if($mode -eq 'legacy'){$oldExe}else{$exe}) $target
  $caseConfig=Join-Path $case 'data';Seed-QA $caseConfig;$running=Start-Native $target $caseConfig;Visible-QA $running "$mode initial frontend visible"
  $handle=[DengQA]::Find($running.Id);[DengQA]::ShowWindow($handle,6)|Out-Null;Wait-QA {![DengQA]::IsWindowVisible($handle) -or [DengQA]::IsIconic($handle)} 'minimized or hidden before update';Start-Sleep -Seconds 2
  $wasTray=![DengQA]::IsWindowVisible($handle);$report["$mode-beforeUpdateHidden"]=$wasTray;Check-QA $wasTray "$mode minimized into system tray before update"
  $job=Join-Path $caseConfig '.update-1001';New-Item -ItemType Directory $job|Out-Null;$staged=Join-Path $job 'up.exe';Copy-Item $exe $staged;$helper=Join-Path $job 'dengshell-updater-1001.exe';Copy-Item $target $helper
  $plan=@{schema=2;build=$(if($mode -eq 'legacy'){$build}else{$build+1});version='v0.01';parentPID=$running.Id;target=$target;staged=$staged;configDir=$caseConfig;oldSHA256=(Get-FileHash $target).Hash.ToLowerInvariant();packageSHA256=$expected;executableSHA256=$expected;platform='windows-amd64'}
  $planFile=Join-Path $job 'plan.json';[IO.File]::WriteAllText($planFile,($plan|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
  $worker=Start-Process $helper -ArgumentList @('--dengshell-update-helper',('"'+$planFile+'"')) -WindowStyle Hidden -PassThru
  $readyWatch=[Diagnostics.Stopwatch]::StartNew()
  $readyBudget=$(if($mode -eq 'legacy'){4}else{60})
  Wait-QA {Test-Path (Join-Path $job 'ready')} "$mode helper validates actual update within client preparation budget" $readyBudget
  $report["$mode-helperReadyMilliseconds"]=$readyWatch.ElapsedMilliseconds
  Remove-Item (Join-Path $caseConfig 'runtime-success-v1-windows-amd64') -ErrorAction SilentlyContinue;Stop-QA $running
  Wait-QA {Test-Path (Join-Path $caseConfig 'update-result.json')} "$mode helper completes replacement"
  Check-QA ((Get-Content (Join-Path $caseConfig 'update-result.json') -Raw | ConvertFrom-Json).status -eq 'installed') "$mode update reports successful installation"
  Wait-QA {Test-Path (Join-Path $caseConfig 'runtime-success-v1-windows-amd64')} "$mode updated native frontend ready"
  $running=Get-Process DengShell -ErrorAction SilentlyContinue|Where-Object {$_.Path -eq $target}|Select-Object -First 1;Check-QA ($null -ne $running) "$mode automatically restarts program";Visible-QA $running "$mode restarted main window visible and not minimized"
  Check-QA ((Get-FileHash $target).Hash.ToLowerInvariant() -eq $expected) "$mode update target hash matches release";Check-QA ((Get-Content (Join-Path $caseConfig 'keep-user-data.txt')).Trim() -eq 'preserve-local-fixture') "$mode update preserves config directory"
  Wait-QA {!(Test-Path $job)} "$mode update cache reclaimed after helper exits" 50
  $backup=Join-Path $caseConfig 'update-backup/previous-program.exe'
  Check-QA ((Test-Path $backup) -and (Get-FileHash $backup).Hash.ToLowerInvariant() -eq $plan.oldSHA256) "$mode retains one verified rollback backup"
  Check-QA (@(Get-ChildItem $caseConfig -Directory -Filter '.update-*').Count -eq 0) "$mode leaves no update staging directories"
  Stop-QA $running
 }
 $report.passed=$true
}finally{
 Get-CimInstance Win32_Process|Where-Object {$_.ExecutablePath -and $_.ExecutablePath.StartsWith($qaRoot,[StringComparison]::OrdinalIgnoreCase)}|ForEach-Object {Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue}
 $report | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $Output 'windows-validation.json')
 Get-ChildItem $qaRoot -Recurse -Filter error.txt | ForEach-Object {Copy-Item $_.FullName (Join-Path $Output ($_.Directory.Parent.Name+'-update-error.txt'))}
}
