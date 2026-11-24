# Removes one third-party antivirus / security suite. Microsoft Defender takes over again afterwards.
$n = $env:PCM_name
$s = @(Get-SecuritySoftware | Where-Object { $_.name -eq $n }) | Select-Object -First 1
if (-not $s) { throw "'$n' isn't installed (the name must match the scan exactly). Rescan and try again." }
$e = $s.entry; $tool = Get-AvRemovalTool $n
$quiet = $true
if ($e.QuietUninstallString) {
  Say ('Running the quiet uninstaller: ' + $e.QuietUninstallString)
  $p = Start-Process -FilePath $env:ComSpec -ArgumentList '/s', '/c', ('"' + $e.QuietUninstallString + '"') -PassThru -WindowStyle Hidden
} elseif ("$($e.UninstallString)" -match 'msiexec' -and $e.PSChildName -match '^\{') {
  Say ('Running: msiexec /x ' + $e.PSChildName + ' /qn')
  $p = Start-Process msiexec.exe -ArgumentList '/x', $e.PSChildName, '/qn', '/norestart' -PassThru
} else {
  # Antivirus uninstallers are protected against silent removal, so open the vendor's own uninstaller.
  $quiet = $false
  Say "ACTION NEEDED ON THE PC: $n's own uninstaller is opening on the screen."
  Say "Choose Uninstall / Remove, and say no to offers to keep protection or install other products."
  # Start it directly (not through a hidden console) so its window is visible.
  $u = "$($e.UninstallString)".Trim()
  $m = [regex]::Match($u, '^"([^"]+)"\s*(.*)$')
  if (-not $m.Success) { $m = [regex]::Match($u, '(?i)^(.+?\.exe)\s*(.*)$') }
  if (-not $m.Success) { throw "Couldn't read the uninstall command for '$n': $u" }
  $p = if ($m.Groups[2].Value) { Start-Process -FilePath $m.Groups[1].Value -ArgumentList $m.Groups[2].Value -PassThru } else { Start-Process -FilePath $m.Groups[1].Value -PassThru }
}
$t0 = Get-Date; $last = $t0
while ($true) {
  $gone = -not @(Get-SecuritySoftware | Where-Object { $_.name -eq $n }).Count
  if ($gone) { break }
  if ($quiet -and $p.HasExited -and ((Get-Date) - $t0).TotalSeconds -gt 20) { break }
  if (((Get-Date) - $t0).TotalMinutes -gt 35) { break }
  if (((Get-Date) - $last).TotalSeconds -ge 60) {
    Say ("Waiting for the uninstaller... {0:N0} min" -f ((Get-Date) - $t0).TotalMinutes + $(if (-not $quiet) { ' (finish it on the PC screen)' } else { '' }))
    $last = Get-Date
  }
  Start-Sleep -Seconds 5
}
$still = @(Get-SecuritySoftware | Where-Object { $_.name -eq $n }).Count -gt 0
if ($still) {
  throw "'$n' is still installed. Restart the PC and try again, or use $tool from the vendor's website, then restart."
}
# Microsoft Defender switches back on when no other antivirus is registered. Refresh its definitions now.
Say 'Checking Microsoft Defender...'
try { Update-MpSignature -ErrorAction Stop; Say 'Defender definitions updated.' } catch { Say 'Defender definitions will update after the restart.' }
$mp = Get-MpComputerStatus -ErrorAction SilentlyContinue
$on = [bool]($mp -and $mp.AntivirusEnabled -and $mp.RealTimeProtectionEnabled)
Out-Result @{ name = $n; removed = $true; rebootRequired = $true; defenderActive = $on; cleanupTool = $tool
  note = if ($on) { 'Removed. Microsoft Defender is protecting the PC. Restart to finish.' } else { "Removed. Restart now: Microsoft Defender switches back on after the restart. If it doesn't, run $tool to clear leftovers." } }
