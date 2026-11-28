# Drive surface test (READ-ONLY, never writes): reads the physical drive to find unreadable sectors
# and regions that have gone slow - the signs of a failing HDD/SSD. mode=sample spans the whole
# drive quickly; mode=full reads everything (slow). The Stop button ends it.
$sample = "$env:PCM_mode" -ne 'full'
$only = "$env:PCM_device"
$drives = @(Get-CimInstance Win32_DiskDrive -ErrorAction SilentlyContinue | Where-Object { $_.Size -and (-not $only -or $_.DeviceID -eq $only) })
if (-not $drives.Count) { throw "No drives found to test." }
$results = @()
foreach ($d in $drives) {
  $dev = $d.DeviceID
  $size = [int64]$d.Size
  $kind = if ($d.MediaType -match 'SSD' -or $d.Model -match 'NVME|SSD') { 'SSD' } else { 'HDD' }
  Say ("Testing $dev ($kind, $([math]::Round($size/1GB)) GB, $($d.Model)) - " + $(if ($sample) { 'quick surface scan' } else { 'full read test' }) + '...')
  $chunk = 4MB
  $step = if ($sample -and ($size / $chunk) -gt 300) { [int64]([math]::Floor($size / 300 / 512) * 512) } else { $chunk }
  if ($step -lt $chunk) { $step = $chunk }
  $buf = New-Object byte[] $chunk
  $errs = 0; $slow = 0; $readBytes = [int64]0; $minMBps = [double]::PositiveInfinity; $bad = @()
  $sw = [Diagnostics.Stopwatch]::StartNew(); $lastLog = [Diagnostics.Stopwatch]::StartNew()
  try {
    $fs = New-Object IO.FileStream($dev, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
  } catch { Say "Can't open $dev (need admin): $($_.Exception.Message)"; $results += [ordered]@{ device = $dev; error = "$($_.Exception.Message)"; kind = $kind }; continue }
  try {
    for ($off = [int64]0; $off -lt $size; $off += $step) {
      if ($off + $chunk -gt $size) { break }
      $t = [Diagnostics.Stopwatch]::StartNew()
      try {
        $fs.Seek($off, 'Begin') | Out-Null
        $n = $fs.Read($buf, 0, $chunk)
        $t.Stop()
        $readBytes += $n
        $sec = $t.Elapsed.TotalSeconds
        if ($sec -gt 0) { $mbps = ($n / 1MB) / $sec; if ($mbps -lt $minMBps) { $minMBps = $mbps }; if ($mbps -lt 10) { $slow++ } }
      } catch {
        $errs++; if ($bad.Count -lt 50) { $bad += ("{0:N1} GB" -f ($off / 1GB)) }
        Say ("Read error at {0:N1} GB on $dev" -f ($off / 1GB))
      }
      if ($lastLog.Elapsed.TotalSeconds -gt 5) { $lastLog.Restart(); Say ("{0}: {1:N0}% scanned, {2} error(s), slowest {3:N0} MB/s" -f $dev, ($off / $size * 100), $errs, $minMBps) }
    }
  } finally { $fs.Close() }
  $sw.Stop()
  $avg = if ($sw.Elapsed.TotalSeconds -gt 0) { ($readBytes / 1MB) / $sw.Elapsed.TotalSeconds } else { 0 }
  $verdict = if ($errs -gt 0) { 'FAILING (read errors)' } elseif ($slow -gt 3) { 'suspect (slow regions)' } else { 'healthy' }
  Say ("{0}: {1} - {2} error(s), {3} slow region(s), avg {4:N0} MB/s." -f $dev, $verdict, $errs, $slow, $avg)
  $results += [ordered]@{ device = $dev; model = "$($d.Model)"; kind = $kind; size = "$([math]::Round($size/1GB)) GB"; scannedGB = [math]::Round($readBytes/1GB, 1); readErrors = $errs; errorOffsets = $bad; slowRegions = $slow; minMBps = $(if ($minMBps -lt [double]::PositiveInfinity) { [math]::Round($minMBps, 1) } else { $null }); avgMBps = [math]::Round($avg, 1); verdict = $verdict; mode = $(if ($sample) { 'sample' } else { 'full' }) }
}
Out-Result @{ drives = $results; mode = $(if ($sample) { 'sample' } else { 'full' }) }
