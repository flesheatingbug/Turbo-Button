$bdir = Backup-Dir; $ts = Get-Date -Format 'yyyyMMdd-HHmmss'
$keys = 'HKLM\SOFTWARE\Policies\Google\Chrome', 'HKCU\SOFTWARE\Policies\Google\Chrome', 'HKLM\SOFTWARE\Policies\Microsoft\Edge', 'HKCU\SOFTWARE\Policies\Microsoft\Edge', 'HKLM\SOFTWARE\Policies\BraveSoftware\Brave', 'HKCU\SOFTWARE\Policies\BraveSoftware\Brave', 'HKLM\SOFTWARE\WOW6432Node\Policies\Google\Chrome'
$removed = @()
foreach ($k in $keys) {
  $ps = 'Registry::' + $k
  if (-not (Test-Path $ps)) { continue }
  $file = Join-Path $bdir ("policy-" + ($k -replace '[\\ ]', '_') + "-$ts.reg")
  & reg.exe export $k $file /y | Out-Null
  Remove-Item $ps -Recurse -Force -ErrorAction Stop
  $removed += $k; Say "Removed $k (backup: $file)"
}
Out-Result @{ removed = $removed; backups = $bdir; note = if ($removed.Count) { 'Restart the browsers. Restore a backup by double-clicking its .reg file.' } else { 'No browser policies found.' } }
