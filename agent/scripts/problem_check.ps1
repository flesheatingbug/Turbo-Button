# Read-only look into the customer's reported problem. PCM_terms holds keywords taken from the
# technician's description (e.g. "onedrive sign login"). Collects matching warnings/errors from
# the last 7 days, matching processes, services and programs, and for sign-in/network problems
# the account join state, the clock and whether Microsoft's sign-in servers are reachable.
$terms = @("$env:PCM_terms".ToLower() -split '\s+' | Where-Object { $_.Length -ge 3 } | Select-Object -Unique -First 10)
if (-not $terms.Count) { throw 'No keywords to look for. Describe the problem in a few words.' }
$rx = ($terms | ForEach-Object { [regex]::Escape($_) }) -join '|'
Say ('Looking for: ' + ($terms -join ', '))
$out = [ordered]@{ terms = $terms }

# ---- Event logs (last 7 days, warnings and errors) ----
$logs = @('Application', 'System')
$extra = @{ 'aad|sign|login|logon|account|onedrive|outlook|teams|office|password|token|credential' = @('Microsoft-Windows-AAD/Operational', 'Microsoft-Windows-User Device Registration/Admin');
  'print|spool' = @('Microsoft-Windows-PrintService/Admin');
  'wifi|wlan|wireless|internet|network|dns|dhcp' = @('Microsoft-Windows-WLAN-AutoConfig/Operational', 'Microsoft-Windows-DNS-Client/Operational');
  'onedrive|sync' = @('Microsoft-Windows-OneDrive/Operational') }
foreach ($k in $extra.Keys) { if ($terms -match $k) { $logs += $extra[$k] } }
$since = (Get-Date).AddDays(-7)
$events = @()
foreach ($l in ($logs | Select-Object -Unique)) {
  Say "Reading $l..."
  try { $events += @(Get-WinEvent -FilterHashtable @{ LogName = $l; Level = 1, 2, 3; StartTime = $since } -MaxEvents 3000 -ErrorAction Stop) } catch {}
}
$hits = @($events | Where-Object { $_.ProviderName -match $rx -or $_.LogName -match $rx -or "$($_.Message)" -match $rx -or $_.LogName -match 'AAD|Device Registration|OneDrive|PrintService|WLAN' })
$out.events = @($hits | Group-Object { "$($_.LogName)|$($_.ProviderName)|$($_.Id)" } | Sort-Object Count -Descending | Select-Object -First 25 | ForEach-Object {
  $e = $_.Group | Sort-Object TimeCreated -Descending | Select-Object -First 1
  [ordered]@{ log = $e.LogName; source = $e.ProviderName; id = $e.Id; level = $e.LevelDisplayName; count = $_.Count; last = $e.TimeCreated.ToString('s')
    message = (("$($e.Message)" -replace '\s+', ' ').Trim() | ForEach-Object { if ($_.Length -gt 400) { $_.Substring(0, 400) + '…' } else { $_ } }) } })
Say "$($hits.Count) matching warning/error event(s), $($out.events.Count) kinds."

# ---- Processes, services, programs ----
$out.processes = @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -match $rx -or "$($_.Path)" -match $rx } | Select-Object -First 15 | ForEach-Object {
  [ordered]@{ name = $_.ProcessName; pid = $_.Id; memMB = [math]::Round($_.WorkingSet64 / 1MB); path = "$($_.Path)"; started = $(try { $_.StartTime.ToString('s') } catch { $null }) } })
$out.services = @(Get-CimInstance Win32_Service -OperationTimeoutSec 20 -ErrorAction SilentlyContinue | Where-Object { $_.Name -match $rx -or $_.DisplayName -match $rx -or "$($_.PathName)" -match $rx } | Select-Object -First 15 | ForEach-Object {
  [ordered]@{ name = $_.Name; display = $_.DisplayName; state = $_.State; start = $_.StartMode } })
$out.programs = @(Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
  Where-Object { $_.DisplayName -and $_.DisplayName -match $rx } | Select-Object -First 15 | ForEach-Object { [ordered]@{ name = $_.DisplayName; version = $_.DisplayVersion; installed = $_.InstallDate } })
$out.storeApps = @(Get-AppxPackage -AllUsers -ErrorAction SilentlyContinue | Where-Object { $_.Name -match $rx } | Select-Object -First 10 | ForEach-Object { [ordered]@{ name = $_.Name; version = "$($_.Version)"; status = "$($_.Status)" } })
Say "$($out.processes.Count) running process(es), $($out.services.Count) service(s), $($out.programs.Count + $out.storeApps.Count) program(s) match."

# ---- Sign-in problems: join state, clock, reachability ----
if ($terms -match 'sign|login|logon|account|onedrive|outlook|teams|office|password|token|credential|365|microsoft|activat') {
  Say 'Checking account join state, the clock and Microsoft sign-in servers...'
  try {
    $ds = & "$env:windir\System32\dsregcmd.exe" /status 2>$null
    $keep = 'AzureAdJoined|EnterpriseJoined|DomainJoined|WorkplaceJoined|AzureAdPrt |AzureAdPrtUpdateTime|TenantName|DeviceAuthStatus|WamDefaultSet|NgcSet|AttemptStatus|Server Error Code|Diagnostic Data'
    $out.accountJoin = @($ds | Where-Object { $_ -match $keep } | ForEach-Object { ($_ -replace '\s+', ' ').Trim() } | Select-Object -First 20)
  } catch {}
  try {
    $w = & "$env:windir\System32\w32tm.exe" /query /status 2>$null
    $out.clock = [ordered]@{ now = (Get-Date).ToString('s'); timeZone = (Get-TimeZone).Id
      lastSync = (($w | Where-Object { $_ -match 'Last Successful Sync Time' }) -replace '^[^:]+:\s*', '') ; source = (($w | Where-Object { $_ -match '^Source' }) -replace '^[^:]+:\s*', '') }
  } catch {}
  $out.reachable = @(foreach ($h in 'login.microsoftonline.com', 'login.live.com', 'graph.microsoft.com') {
    $ok = $false; $ms = $null
    try { $c = New-Object Net.Sockets.TcpClient; $sw = [Diagnostics.Stopwatch]::StartNew(); $t = $c.ConnectAsync($h, 443); $ok = $t.Wait(4000) -and $c.Connected; $ms = $sw.ElapsedMilliseconds; $c.Close() } catch {}
    [ordered]@{ host = $h; ok = $ok; ms = $(if ($ok) { $ms } else { $null }) } })
  $px = Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
  $out.proxy = [ordered]@{ enabled = [bool]$px.ProxyEnable; server = "$($px.ProxyServer)"; autoConfig = "$($px.AutoConfigURL)"; winhttp = ((& netsh winhttp show proxy 2>$null) -join ' ' -replace '\s+', ' ').Trim() }
  # Cached sign-ins for Microsoft accounts/apps (names of the entries only, never secrets).
  $out.credentialEntries = @((& cmdkey /list 2>$null) | Where-Object { $_ -match 'Target:' -and $_ -match 'MicrosoftOffice|OneDrive|msteams|MicrosoftAccount|virtualapp/didlogical|AzureAD' } | ForEach-Object { ($_ -replace '^\s*Target:\s*', '').Trim() } | Select-Object -First 15)
}
if ($terms -match 'onedrive') {
  $od = @(Get-ChildItem "$env:SystemDrive\Users\*\AppData\Local\Microsoft\OneDrive\OneDrive.exe", "$env:ProgramFiles\Microsoft OneDrive\OneDrive.exe", "${env:ProgramFiles(x86)}\Microsoft OneDrive\OneDrive.exe" -ErrorAction SilentlyContinue)
  $pol = Get-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\OneDrive', 'HKLM:\SOFTWARE\Policies\Microsoft\OneDrive' -ErrorAction SilentlyContinue
  $out.oneDrive = [ordered]@{ installs = @($od | ForEach-Object { [ordered]@{ path = $_.FullName; version = $_.VersionInfo.FileVersion } }); running = [bool](Get-Process OneDrive -ErrorAction SilentlyContinue)
    disabledByPolicy = [bool]($pol | Where-Object { $_.DisableFileSyncNGSC -eq 1 -or $_.DisableFileSync -eq 1 }); policies = @($pol | ForEach-Object { $_.PSObject.Properties | Where-Object { $_.Name -notmatch '^PS' } | ForEach-Object { "$($_.Name)=$($_.Value)" } }) }
}
Out-Result $out
