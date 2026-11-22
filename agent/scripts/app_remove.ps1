$n = $env:PCM_name
$pk = @(Get-AppxPackage -AllUsers -Name $n -ErrorAction SilentlyContinue)
$pp = @(Get-AppxProvisionedPackage -Online -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $n })
if (-not $pk.Count -and -not $pp.Count) { throw "App '$n' not found" }
$errs = @()
foreach ($p in $pk) {
  if ($p.NonRemovable) { throw "'$n' is marked non-removable by Windows" }
  try { Remove-AppxPackage -Package $p.PackageFullName -AllUsers -ErrorAction Stop } catch { $errs += $_.Exception.Message }
}
foreach ($p in $pp) { try { Remove-AppxProvisionedPackage -Online -PackageName $p.PackageName -ErrorAction Stop | Out-Null } catch { $errs += $_.Exception.Message } }
$left = @(Get-AppxPackage -AllUsers -Name $n -ErrorAction SilentlyContinue).Count
if ($left -gt 0 -and $errs.Count) { throw ("Removal incomplete: " + ($errs -join ' ; ')) }
Out-Result @{ name = $n; removedPackages = $pk.Count; removedProvisioned = $pp.Count; warnings = $errs }
