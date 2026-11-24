# --- Turbo Button prelude ---
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch {}
trap { Write-Output ("##ERROR##" + $_.Exception.Message); exit 1 }

function Out-Result($o) { Write-Output ("##RESULT##" + ($o | ConvertTo-Json -Compress -Depth 8)) }
function Say($m) { Write-Output $m }

function Get-PathSize([string[]]$paths) {
  $sum = [int64]0
  foreach ($p in $paths) {
    foreach ($f in @(Get-ChildItem -Path $p -Recurse -Force -File -ErrorAction SilentlyContinue)) { $sum += $f.Length }
  }
  return $sum
}

function Clear-Paths([string[]]$paths) {
  $before = Get-PathSize $paths
  foreach ($p in $paths) {
    Get-Item -Path $p -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
  }
  $after = Get-PathSize $paths
  return @{ freedBytes = [int64]($before - $after); remainingBytes = [int64]$after; note = 'Files in use are skipped.' }
}

$CleanupTargets = @{
  user_temp             = @('C:\Users\*\AppData\Local\Temp\*')
  windows_temp          = @("$env:windir\Temp\*")
  update_cache          = @("$env:windir\SoftwareDistribution\Download\*")
  delivery_optimization = @("$env:windir\ServiceProfiles\NetworkService\AppData\Local\Microsoft\Windows\DeliveryOptimization\Cache\*", "$env:windir\SoftwareDistribution\DeliveryOptimization\*")
  crash_dumps           = @("$env:windir\Minidump\*", "$env:windir\MEMORY.DMP", 'C:\Users\*\AppData\Local\CrashDumps\*', "$env:ProgramData\Microsoft\Windows\WER\ReportArchive\*", "$env:ProgramData\Microsoft\Windows\WER\ReportQueue\*")
  browser_cache         = @(
    'C:\Users\*\AppData\Local\Google\Chrome\User Data\*\Cache\*', 'C:\Users\*\AppData\Local\Google\Chrome\User Data\*\Code Cache\*', 'C:\Users\*\AppData\Local\Google\Chrome\User Data\*\GPUCache\*',
    'C:\Users\*\AppData\Local\Microsoft\Edge\User Data\*\Cache\*', 'C:\Users\*\AppData\Local\Microsoft\Edge\User Data\*\Code Cache\*', 'C:\Users\*\AppData\Local\Microsoft\Edge\User Data\*\GPUCache\*',
    'C:\Users\*\AppData\Local\BraveSoftware\Brave-Browser\User Data\*\Cache\*', 'C:\Users\*\AppData\Local\BraveSoftware\Brave-Browser\User Data\*\Code Cache\*',
    'C:\Users\*\AppData\Local\Mozilla\Firefox\Profiles\*\cache2\*')
  thumbnail_cache       = @('C:\Users\*\AppData\Local\Microsoft\Windows\Explorer\thumbcache_*.db', 'C:\Users\*\AppData\Local\Microsoft\Windows\Explorer\iconcache_*.db')
  recycle_bin           = @()
}
function Get-RecycleSize {
  $s = [int64]0
  foreach ($v in @(Get-Volume -ErrorAction SilentlyContinue | Where-Object { $_.DriveLetter })) {
    $s += Get-PathSize @("$($v.DriveLetter):\`$Recycle.Bin\*")
  }
  return $s
}
# --- end prelude ---

# --- shared helpers (v0.3) ---
function Get-Winget {
  $c = Get-Command winget.exe -ErrorAction SilentlyContinue
  if ($c) { return $c.Source }
  $w = Get-ChildItem "$env:ProgramFiles\WindowsApps\Microsoft.DesktopAppInstaller_*_x64__8wekyb3d8bbwe\winget.exe" -ErrorAction SilentlyContinue | Sort-Object FullName -Descending | Select-Object -First 1
  if ($w) { return $w.FullName }
  return $null
}
# Last visible text of a console line (drops spinner/progress-bar redraws).
function Clean-Line($l) {
  $t = ("$l" -split "`r")[-1]
  $t = ($t -replace '[\u2580-\u259F]', '').Trim()
  if ($t -match '^[\-\\|/ ]*$') { return $null }
  if ($t -match '^\d+(\.\d+)?\s*(B|KB|MB|GB)\s*/\s*\d') { return $null }
  return $t
}
function Backup-Dir {
  $d = "$env:ProgramData\TurboButton\backups"
  New-Item -ItemType Directory -Path $d -Force | Out-Null
  return $d
}
$UninstallKeys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'

# ---- Office and security-software detection (shared by scan, office_remove, av_remove) ----
$OfficeNameRe = '^(Microsoft (Office|365)|Microsoft (Word|Excel|PowerPoint|Outlook|Access|Publisher|Visio|Project|OneNote) (19|20)\d\d|Office 16 Click-to-Run)'
$AvVendorRe = '(McAfee|Norton|Symantec|Avast|AVG |AVG$|Avira|Kaspersky|ESET|Bitdefender|Trend Micro|Sophos|Malwarebytes|Panda (Dome|Security|Free)|TotalAV|Total AV|Webroot|F-Secure|G DATA|Comodo|360 Total Security|BullGuard|ZoneAlarm|Quick Heal|Total Defense|Scanguard|PC Matic|VIPRE|Emsisoft|Surfshark Antivirus|Intego|K7 ?(Total|Anti))'
$AvSkipRe = '(Browser|VPN|Password|SafeZone|Secure Browser|Driver Updater|Cleanup|Tune ?Up|Utilities|Family|Parental|Toolbar|Search|Web Advisor|WebAdvisor|Safe ?Connect|Identity|Privacy|PC Optimizer|Speedup|Support Tool|Update Helper)'
function Get-UninstallEntries {
  @(Get-ItemProperty $UninstallKeys -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -and -not $_.ParentKeyName })
}
function Get-OfficeInstalls {
  @(Get-UninstallEntries | Where-Object { $_.DisplayName -match $OfficeNameRe -and $_.DisplayName -notmatch '(MUI|Proofing|Language Pack|Shared|Components|Click-to-Run (Extensibility|Licensing|Localization)|Interop|Runtime|Access database engine|Visual Studio)' -and $_.SystemComponent -ne 1 -and $_.UninstallString } |
    Sort-Object DisplayName -Unique | ForEach-Object {
      $u = "$($_.UninstallString)"
      $kind = if ($u -match 'OfficeClickToRun\.exe') { 'click-to-run' } elseif ($u -match '(?i)setup\.exe.*?/uninstall\s+\S+') { 'msi-setup' } elseif ($u -match '(?i)msiexec' -and $_.PSChildName -match '^\{') { 'msi' } else { 'other' }
      [pscustomobject]@{ name = $_.DisplayName; version = $_.DisplayVersion; kind = $kind; entry = $_ } })
}
function Get-SecuritySoftware {
  @(Get-UninstallEntries | Where-Object { ($_.DisplayName -match $AvVendorRe -or $_.Publisher -match $AvVendorRe) -and $_.DisplayName -notmatch $AvSkipRe -and $_.DisplayName -notmatch 'Defender|Microsoft' -and $_.SystemComponent -ne 1 -and ($_.UninstallString -or $_.QuietUninstallString) } |
    Sort-Object DisplayName -Unique | ForEach-Object {
      $how = if ($_.QuietUninstallString) { 'quiet' } elseif ("$($_.UninstallString)" -match 'msiexec' -and $_.PSChildName -match '^\{') { 'msi' } else { 'interactive' }
      [pscustomobject]@{ name = $_.DisplayName; publisher = $_.Publisher; version = $_.DisplayVersion; uninstall = $how; entry = $_ } })
}
# The vendor's own clean-up tool, for when the normal uninstaller fails or is blocked.
function Get-AvRemovalTool($n) {
  switch -Regex ($n) {
    'McAfee' { 'McAfee Consumer Product Removal tool (MCPR)' }
    'Norton|Symantec' { 'Norton Remove and Reinstall tool' }
    'Avast' { 'Avast Clear (avastclear.exe)' }
    'AVG' { 'AVG Clear (avgclear.exe)' }
    'Kaspersky' { 'Kaspersky kavremover' }
    'ESET' { 'ESET Uninstaller' }
    'Bitdefender' { 'Bitdefender Uninstall Tool' }
    'Malwarebytes' { 'Malwarebytes Support Tool (Clean option)' }
    'Trend Micro' { 'Trend Micro Uninstall Tool' }
    default { "the vendor's removal tool (search the vendor's support site)" }
  }
}
