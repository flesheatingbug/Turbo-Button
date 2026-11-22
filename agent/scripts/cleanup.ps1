$t = $env:PCM_target
switch ($t) {
  'recycle_bin' {
    $before = Get-RecycleSize
    Clear-RecycleBin -Force -ErrorAction SilentlyContinue
    $after = Get-RecycleSize
    Out-Result @{ target = $t; freedBytes = [int64]($before - $after); remainingBytes = $after }
    break
  }
  'update_cache' {
    Say 'Stopping Windows Update services...'
    Stop-Service wuauserv, bits -Force -ErrorAction SilentlyContinue
    $res = Clear-Paths $CleanupTargets[$t]
    Start-Service bits, wuauserv -ErrorAction SilentlyContinue
    $res.target = $t; Out-Result $res
    break
  }
  'delivery_optimization' {
    $before = Get-PathSize $CleanupTargets[$t]
    try { Delete-DeliveryOptimizationCache -Force -ErrorAction Stop } catch { $null = Clear-Paths $CleanupTargets[$t] }
    $after = Get-PathSize $CleanupTargets[$t]
    Out-Result @{ target = $t; freedBytes = [int64]($before - $after); remainingBytes = $after }
    break
  }
  default {
    if (-not $CleanupTargets.ContainsKey($t)) { throw "Unknown target $t" }
    $res = Clear-Paths $CleanupTargets[$t]; $res.target = $t; Out-Result $res
  }
}
