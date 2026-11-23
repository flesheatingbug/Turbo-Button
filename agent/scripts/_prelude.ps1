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
