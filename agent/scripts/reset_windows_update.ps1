$svcs = 'wuauserv', 'bits', 'cryptsvc', 'msiserver', 'usosvc'
Say 'Stopping update services...'
foreach ($s in $svcs) { Stop-Service -Name $s -Force -ErrorAction SilentlyContinue }
Start-Sleep -Seconds 2
$ts = Get-Date -Format 'yyyyMMddHHmmss'; $steps = @()
foreach ($d in "$env:windir\SoftwareDistribution", "$env:windir\System32\catroot2") {
  if (-not (Test-Path $d)) { continue }
  $new = (Split-Path $d -Leaf) + ".old-$ts"
  try { Rename-Item -Path $d -NewName $new -ErrorAction Stop; $steps += "Renamed $d to $new" } catch { $steps += "Could not rename $d : $($_.Exception.Message)" }
  Say $steps[-1]
}
Remove-Item "$env:ProgramData\Microsoft\Network\Downloader\qmgr*.dat" -Force -ErrorAction SilentlyContinue
Say 'Starting update services...'
foreach ($s in 'cryptsvc', 'bits', 'wuauserv') { Start-Service -Name $s -ErrorAction SilentlyContinue }
Out-Result @{ steps = $steps; note = 'Old folders kept as *.old-<date>. Run Windows Update next.' }
