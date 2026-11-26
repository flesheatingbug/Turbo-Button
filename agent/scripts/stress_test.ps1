# Stress test: loads the CPU (and optionally RAM) hard for a set time to shake out overheating,
# throttling and instability. Read-only — changes nothing. Watches temperature and clock speed,
# and STOPS EARLY if the CPU reaches 97 C, to protect the hardware. The load runs in child
# processes, so the Stop button (which kills the job's process tree) ends it cleanly.
$dur = [Math]::Max(1, [int]"$env:PCM_minutes") * 60
$target = if ("$env:PCM_target") { "$env:PCM_target" } else { 'cpu' }
$cores = [Environment]::ProcessorCount
$deadline = (Get-Date).AddSeconds($dur)
$iso = $deadline.ToString('o')

function Get-TempC {
  try {
    $z = Get-CimInstance -Namespace root/WMI -ClassName MSAcpi_ThermalZoneTemperature -OperationTimeoutSec 5 -ErrorAction Stop
    $t = @($z | ForEach-Object { [math]::Round(($_.CurrentTemperature / 10) - 273.15, 1) } | Where-Object { $_ -gt 10 -and $_ -lt 125 })
    if ($t.Count) { return ($t | Measure-Object -Maximum).Maximum }
  } catch {}
  return $null
}
function Get-Clock {
  try { $c = Get-CimInstance Win32_Processor -OperationTimeoutSec 5 -ErrorAction Stop | Select-Object -First 1
    if ($c.MaxClockSpeed) { return [pscustomobject]@{ now = [int]$c.CurrentClockSpeed; max = [int]$c.MaxClockSpeed; pct = [int][math]::Round(100 * $c.CurrentClockSpeed / $c.MaxClockSpeed) } } } catch {}
  return $null
}
function Enc([string]$code) { [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code)) }

$idleTemp = Get-TempC
$baseClock = Get-Clock
Say ("Stress test: $target for $([int]($dur/60)) min on $cores logical processor(s)." + $(if ($idleTemp) { " Idle temperature ${idleTemp} C." } else { " (No temperature sensor via Windows; watching clock speed and stability.)" }))

$procs = @()
# CPU workers: one tight floating-point loop per logical processor, each with its own deadline.
if ($target -ne 'memory') {
  $cpuCode = @"
`$e=[datetime]::Parse('$iso'); `$x=0.0001
while((Get-Date) -lt `$e){ for(`$k=0;`$k -lt 400000;`$k++){ `$x=[math]::Sqrt(`$x*1.0000001+1.2345); if(`$x -gt 1e9){`$x=0.0001} } }
"@
  $enc = Enc $cpuCode
  for ($i = 0; $i -lt $cores; $i++) { $procs += Start-Process powershell -WindowStyle Hidden -PassThru -ArgumentList '-NoProfile', '-EncodedCommand', $enc }
  Say "Started $cores CPU worker(s)."
}
# Memory worker: fills a chunk of free RAM and keeps writing to it (catches bad RAM / low memory).
if ($target -eq 'memory' -or $target -eq 'all') {
  $freeMB = 2048
  try { $os = Get-CimInstance Win32_OperatingSystem -OperationTimeoutSec 10; $freeMB = [int]($os.FreePhysicalMemory / 1024) } catch {}
  $useMB = [Math]::Min([Math]::Max(512, [int]($freeMB * 0.6)), 8192)
  $memCode = @"
`$e=[datetime]::Parse('$iso'); `$chunks=@(); `$mb=$useMB; `$n=[int][math]::Ceiling(`$mb/256)
try { for(`$c=0;`$c -lt `$n;`$c++){ `$chunks += ,(New-Object byte[] (256MB)) } } catch {}
while((Get-Date) -lt `$e){ foreach(`$a in `$chunks){ for(`$i=0;`$i -lt `$a.Length;`$i+=4096){ `$a[`$i]=[byte]((`$a[`$i]+1) -band 255) } } }
"@
  $procs += Start-Process powershell -WindowStyle Hidden -PassThru -ArgumentList '-NoProfile', '-EncodedCommand', (Enc $memCode)
  Say "Started a memory worker using about ${useMB} MB."
}

$maxTemp = $idleTemp; $minClockPct = 100; $stoppedEarly = $false; $reason = $null
try {
  while ((Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 5
    $left = [int]((($deadline) - (Get-Date)).TotalSeconds)
    $t = Get-TempC; $c = Get-Clock
    if ($t -ne $null) { if ($maxTemp -eq $null -or $t -gt $maxTemp) { $maxTemp = $t } }
    if ($c -and $c.pct -lt $minClockPct) { $minClockPct = $c.pct }
    Say ("${left}s left" + $(if ($t) { " - temp ${t} C" } else { "" }) + $(if ($c) { " - clock $($c.now) MHz ($($c.pct)% of max)" } else { "" }))
    if ($t -ne $null -and $t -ge 97) { $stoppedEarly = $true; $reason = "CPU reached ${t} C"; Say "CPU reached ${t} C - stopping early to protect the PC."; break }
    if (@($procs | Where-Object { -not $_.HasExited }).Count -eq 0) { Say 'Workers ended.'; break }
  }
} finally {
  foreach ($p in $procs) { try { if (-not $p.HasExited) { $p.Kill() } } catch {} }
}
$ranSec = [int]((Get-Date) - $deadline.AddSeconds(-$dur)).TotalSeconds
$verdict = if ($stoppedEarly) { 'overheated' } elseif ($maxTemp -ne $null -and $maxTemp -ge 90) { 'hot but held' } elseif ($minClockPct -lt 60 -and $baseClock -and $baseClock.max -gt $baseClock.now) { 'throttled' } else { 'stable' }
$note = switch ($verdict) {
  'overheated' { "Stopped early: $reason. Clean the fans and vents and check the thermal paste." }
  'hot but held' { "Ran hot (max ${maxTemp} C) but stayed up. Worth cleaning fans/vents." }
  'throttled' { "CPU clock dropped under load (down to ${minClockPct}% of max) - likely thermal or power throttling." }
  'stable' { if ($maxTemp) { "Held up for the full run (max ${maxTemp} C)." } else { "Held up for the full run without crashing." } }
}
Say $note
Out-Result @{ target = $target; requestedSec = $dur; ranSec = $ranSec; cores = $cores; idleTempC = $idleTemp; maxTempC = $maxTemp; minClockPct = $(if ($minClockPct -le 100) { $minClockPct } else { $null }); tempAvailable = [bool]$idleTemp; stoppedEarly = $stoppedEarly; verdict = $verdict; note = $note }
