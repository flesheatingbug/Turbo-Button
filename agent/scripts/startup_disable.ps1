$map = @{
  HKCU_Run       = @{ key = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'; ap = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run' }
  HKLM_Run       = @{ key = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'; ap = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run' }
  HKLM_Run32     = @{ key = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Run'; ap = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run32' }
  Startup_User   = @{ dir = "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup"; ap = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder' }
  Startup_Common = @{ dir = "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\StartUp"; ap = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder' }
}
$m = $map[$env:PCM_location]; $name = $env:PCM_name
if (-not $m) { throw "Unknown location" }
if ($m.key) { if ($null -eq (Get-ItemProperty $m.key -Name $name -ErrorAction SilentlyContinue)) { throw "No startup entry '$name' in $($env:PCM_location)" } }
else { if (-not (Test-Path -LiteralPath (Join-Path $m.dir $name))) { throw "No file '$name' in startup folder" } }
if (-not (Test-Path $m.ap)) { New-Item -Path $m.ap -Force | Out-Null }
function Set-StartupState([bool]$enabled) {
  $first = if ($enabled) { 2 } else { 3 }
  $bytes = [byte[]]@($first, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
  if (-not $enabled) { [BitConverter]::GetBytes((Get-Date).ToFileTime()).CopyTo($bytes, 4) }
  New-ItemProperty -Path $m.ap -Name $name -Value $bytes -PropertyType Binary -Force | Out-Null
}
Set-StartupState $false; Out-Result @{ location = $env:PCM_location; name = $name; enabled = $false }
