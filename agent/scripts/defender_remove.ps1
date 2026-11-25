# Removes threats Microsoft Defender has found that are still on the PC (not yet quarantined or
# removed), using Defender's own clean-up. Anything the customer chose to "allow" in Windows
# Security is left alone and listed, because that was a deliberate choice.
$before = @(Get-MpThreat -ErrorAction Stop)
$active = @($before | Where-Object { $_.IsActive })
$allowedIds = @{}; foreach ($d in @(Get-MpThreatDetection -ErrorAction SilentlyContinue)) { if ([int]$d.ThreatStatusID -eq 5) { $allowedIds["$($d.ThreatID)"] = $true } }
$allowed = @($active | Where-Object { $allowedIds["$($_.ThreatID)"] })
$toRemove = @($active | Where-Object { -not $allowedIds["$($_.ThreatID)"] })
foreach ($t in $allowed) { Say "Left alone (allowed in Windows Security): $($t.ThreatName)" }
if (-not $toRemove.Count) { Say 'Nothing to remove: Defender has already quarantined or removed everything it found.' }
else {
  foreach ($t in $toRemove) { Say "Removing: $($t.ThreatName)" }
  Remove-MpThreat -ErrorAction Stop
  Start-Sleep 5
}
$still = @(Get-MpThreat -ErrorAction SilentlyContinue | Where-Object { $_.IsActive -and -not $allowedIds["$($_.ThreatID)"] })
foreach ($t in $still) { Say "Still present: $($t.ThreatName) - run a Defender offline scan." }
Out-Result @{ removed = ($toRemove.Count - $still.Count); stillActive = @($still | ForEach-Object { $_.ThreatName }); allowed = @($allowed | ForEach-Object { $_.ThreatName }) }
