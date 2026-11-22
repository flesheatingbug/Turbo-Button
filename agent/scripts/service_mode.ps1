$s = Get-CimInstance Win32_Service -Filter ("Name='{0}'" -f ($env:PCM_name -replace "'", "''")) -ErrorAction Stop
if (-not $s) { throw "Service '$($env:PCM_name)' not found" }
$prev = $s.StartMode
Set-Service -Name $s.Name -StartupType $env:PCM_mode -ErrorAction Stop
if ($env:PCM_mode -eq 'Disabled') { Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue }
$now = Get-CimInstance Win32_Service -Filter ("Name='{0}'" -f ($s.Name -replace "'", "''"))
Out-Result @{ name = $s.Name; displayName = $s.DisplayName; previousMode = $prev; mode = $now.StartMode; state = $now.State }
