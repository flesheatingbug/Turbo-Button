$start = Get-Date
$type = if ($env:PCM_type -eq 'full') { 'FullScan' } else { 'QuickScan' }
Start-MpScan -ScanType $type -ErrorAction Stop
$found = @(Get-MpThreatDetection -ErrorAction SilentlyContinue | Where-Object { $_.InitialDetectionTime -ge $start.AddMinutes(-1) })
$names = @{}; foreach ($t in @(Get-MpThreat -ErrorAction SilentlyContinue)) { $names["$($t.ThreatID)"] = $t.ThreatName }
Out-Result @{ scanType = $type; minutes = [math]::Round(((Get-Date) - $start).TotalMinutes, 1); detections = @($found | ForEach-Object {
  @{ name = $names["$($_.ThreatID)"]; action = "$($_.ThreatStatusID)"; resources = ($_.Resources -join ' ; ') } }) }
