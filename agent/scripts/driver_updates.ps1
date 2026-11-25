# Driver updates: Windows Update's driver catalogue first, then the PC maker's own updater
# (Dell Command Update, Lenovo System Update, HP Image Assistant) when it's installed,
# or when PCM_oemtool=1 and winget can install it. mode=check only lists.
$install = $env:PCM_mode -eq 'install'
$results = @(); $found = @()

# ---- 1. Windows Update drivers ----
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $session.ClientApplicationID = 'Turbo Button'
  Say 'Searching Windows Update for driver updates...'
  $res = $session.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Driver'")
  $list = @(); foreach ($u in $res.Updates) { $list += $u }
  foreach ($u in $list) { Say ('Found: ' + $u.Title); $found += [ordered]@{ title = $u.Title; source = 'Windows Update' } }
  if ($list.Count -eq 0) { Say 'Windows Update has no newer drivers for this PC.' }
  if ($install) {
    $i = 0
    foreach ($u in $list) {
      $i++
      if ($u.InstallationBehavior.CanRequestUserInput) { Say ('Skipping (needs someone at the PC): ' + $u.Title); $results += [ordered]@{ title = $u.Title; result = 'skipped' }; continue }
      if (-not $u.EulaAccepted) { $u.AcceptEula() }
      $one = New-Object -ComObject Microsoft.Update.UpdateColl; [void]$one.Add($u)
      Say ("[$i/$($list.Count)] Downloading: " + $u.Title)
      try { $dl = $session.CreateUpdateDownloader(); $dl.Updates = $one; [void]$dl.Download() } catch {}
      if (-not $u.IsDownloaded) { Say '   download failed'; $results += [ordered]@{ title = $u.Title; result = 'download failed' }; continue }
      Say ("[$i/$($list.Count)] Installing: " + $u.Title)
      try {
        $inst = $session.CreateUpdateInstaller(); $inst.Updates = $one
        $r = $inst.Install(); $code = [int]$r.GetUpdateResult(0).ResultCode
        $txt = @{ 2 = 'installed'; 3 = 'installed with errors'; 4 = 'failed'; 5 = 'aborted' }[$code]; if (-not $txt) { $txt = "result $code" }
        Say ('   ' + $txt); $results += [ordered]@{ title = $u.Title; result = $txt }
      } catch { Say ('   failed: ' + $_.Exception.Message); $results += [ordered]@{ title = $u.Title; result = 'failed' } }
    }
  }
} catch { Say ('Windows Update search failed: ' + $_.Exception.Message) }

# ---- 2. The PC maker's updater ----
$maker = "$((Get-CimInstance Win32_ComputerSystem -OperationTimeoutSec 15 -ErrorAction SilentlyContinue).Manufacturer)"
function Find-First([string[]]$paths) { foreach ($p in $paths) { $f = Get-Item $p -ErrorAction SilentlyContinue | Select-Object -First 1; if ($f) { return $f.FullName } }; return $null }
function Find-Oem {
  switch -Regex ($maker) {
    'Dell' { $p = Find-First @("$env:ProgramFiles\Dell\CommandUpdate\dcu-cli.exe", "${env:ProgramFiles(x86)}\Dell\CommandUpdate\dcu-cli.exe"); return @{ name = 'Dell Command Update'; exe = $p; winget = 'Dell.CommandUpdate' } }
    'Lenovo' { $p = Find-First @("${env:ProgramFiles(x86)}\Lenovo\System Update\Tvsu.exe", "$env:ProgramFiles\Lenovo\System Update\Tvsu.exe"); return @{ name = 'Lenovo System Update'; exe = $p; winget = 'Lenovo.SystemUpdate' } }
    'HP|Hewlett' { $p = Find-First @("$env:ProgramFiles\HP\HPIA\HPImageAssistant.exe", "${env:ProgramFiles(x86)}\HP\HPIA\HPImageAssistant.exe", "$env:SystemDrive\SWSetup\HPIA\HPImageAssistant.exe", "$env:ProgramFiles\HP\HP Image Assistant\HPImageAssistant.exe"); return @{ name = 'HP Image Assistant'; exe = $p; winget = $null } }
  }
  return $null
}
$oem = Find-Oem
$oemResult = $null
if ($oem -and -not $oem.exe -and $install -and $env:PCM_oemtool -eq '1' -and $oem.winget) {
  $wg = Get-Winget
  if ($wg) {
    Say "Installing $($oem.name) with winget..."
    & $wg install --id $oem.winget --exact --silent --accept-package-agreements --accept-source-agreements --disable-interactivity 2>&1 | ForEach-Object { $t = Clean-Line $_; if ($t) { Say $t } }
    $oem = Find-Oem
  } else { Say "winget isn't available, so $($oem.name) can't be installed automatically." }
}
if ($oem -and $oem.exe) {
  $out = @()
  $logDir = Join-Path $env:TEMP 'turbo-oem'; New-Item -ItemType Directory -Force $logDir | Out-Null
  switch ($oem.name) {
    'Dell Command Update' {
      $a = if ($install) { @('/applyUpdates', '-silent', '-reboot=disable', "-outputLog=$logDir\dcu.log") } else { @('/scan', '-silent', "-outputLog=$logDir\dcu.log") }
    }
    'Lenovo System Update' {
      $a = if ($install) { @('/CM', '-search', 'A', '-action', 'INSTALL', '-includerebootpackages', '1,3,4', '-noicon', '-noreboot', '-nolicense') } else { @('/CM', '-search', 'A', '-action', 'LIST', '-noicon', '-nolicense') }
    }
    'HP Image Assistant' {
      $a = if ($install) { @('/Operation:Analyze', '/Action:Install', '/Selection:All', '/Category:Drivers,Firmware', '/Silent', '/Noninteractive', "/ReportFolder:$logDir", "/SoftpaqDownloadFolder:$logDir\sp") } else { @('/Operation:Analyze', '/Action:List', '/Selection:All', '/Category:Drivers,Firmware', '/Silent', '/Noninteractive', "/ReportFolder:$logDir") }
    }
  }
  Say ("Running $($oem.name) (" + $(if ($install) { 'install' } else { 'check only' }) + ')...')
  & $oem.exe @a 2>&1 | ForEach-Object { $t = Clean-Line $_; if ($t) { Say $t; $out += $t } }
  $code = $LASTEXITCODE
  if (Test-Path "$logDir\dcu.log") { Get-Content "$logDir\dcu.log" -Tail 25 -ErrorAction SilentlyContinue | ForEach-Object { $t = Clean-Line $_; if ($t) { $out += $t } } }
  # Dell: 0 = done, 1 = reboot needed, 500 = nothing to do. HP: 0 = done, 256/257 = nothing / reboot. Lenovo: 0.
  $note = switch ($code) { 0 { 'finished' } 1 { 'finished, restart needed' } 5 { 'finished, restart needed' } 500 { 'already up to date' } 256 { 'already up to date' } 3010 { 'finished, restart needed' } default { "exit code $code" } }
  Say "$($oem.name): $note"
  $oemResult = [ordered]@{ tool = $oem.name; exitCode = $code; note = $note; output = (($out | Select-Object -Last 30) -join "`n") }
} elseif ($oem) {
  Say ("$($oem.name) isn't installed." + $(if ($oem.winget) { ' Tick "install the maker''s updater" to have it installed and run.' } else { ' It can be downloaded from the maker''s support site.' }))
}

# ---- 3. What's left: graphics drivers that are old ----
$gpuOld = @(Get-CimInstance Win32_VideoController -OperationTimeoutSec 15 -ErrorAction SilentlyContinue | Where-Object { $_.DriverDate -and $_.DriverDate -lt (Get-Date).AddMonths(-18) -and $_.Name -notmatch 'Basic Display|Remote|Virtual|Hyper-V' } | ForEach-Object { "$($_.Name) (driver from $($_.DriverDate.ToString('yyyy-MM')))" })
foreach ($g in $gpuOld) { Say "Graphics driver still old: $g — NVIDIA/AMD/Intel's own app has newer ones than Windows Update." }

$sysInfo = $null; try { $sysInfo = New-Object -ComObject Microsoft.Update.SystemInfo } catch {}
Out-Result @{ mode = $env:PCM_mode; found = $found.Count; drivers = $found
  installed = @($results | Where-Object { $_.result -like 'installed*' }).Count
  failed = @($results | Where-Object { $_.result -match 'fail|abort' }).Count
  results = $results; oem = $oemResult; oemTool = if ($oem) { $oem.name } else { $null }; oemInstalled = [bool]($oem -and $oem.exe)
  oldGraphics = $gpuOld; rebootRequired = [bool]($sysInfo -and $sysInfo.RebootRequired) }
