$session = New-Object -ComObject Microsoft.Update.Session
$session.ClientApplicationID = 'Turbo Button'
$searcher = $session.CreateUpdateSearcher()
$crit = "IsInstalled=0 and IsHidden=0 and Type='Software'"
if ($env:PCM_drivers -eq '1') { $crit += " or IsInstalled=0 and IsHidden=0 and Type='Driver'" }
Say 'Searching Windows Update (this can take a few minutes)...'
$res = $searcher.Search($crit)
$list = @(); foreach ($u in $res.Updates) { $list += $u }
$info = @($list | ForEach-Object { [ordered]@{ title = $_.Title; kb = (@($_.KBArticleIDs) -join ','); sizeMB = [math]::Round($_.MaxDownloadSize / 1MB); driver = ($_.Type -eq 2) } })
foreach ($i in $info) { Say ('Found: ' + $i.title) }
if ($list.Count -eq 0) { Say 'Windows is up to date.' }
if ($env:PCM_mode -ne 'install' -or $list.Count -eq 0) {
  Out-Result @{ mode = $env:PCM_mode; found = $list.Count; updates = $info; rebootRequired = $false }
  return
}
$coll = New-Object -ComObject Microsoft.Update.UpdateColl
foreach ($u in $list) {
  if ($u.InstallationBehavior.CanRequestUserInput) { Say ('Skipping (needs someone at the PC): ' + $u.Title); continue }
  if (-not $u.EulaAccepted) { $u.AcceptEula() }
  [void]$coll.Add($u)
}
$results = @()
$i = 0
foreach ($u in $coll) {
  $i++
  $one = New-Object -ComObject Microsoft.Update.UpdateColl
  [void]$one.Add($u)
  Say ("[$i/$($coll.Count)] Downloading: " + $u.Title)
  $dl = $session.CreateUpdateDownloader(); $dl.Updates = $one
  try { [void]$dl.Download() } catch { $results += [ordered]@{ title = $u.Title; result = 'download failed' }; Say '   download failed'; continue }
  if (-not $u.IsDownloaded) { $results += [ordered]@{ title = $u.Title; result = 'download failed' }; Say '   download failed'; continue }
  Say ("[$i/$($coll.Count)] Installing: " + $u.Title)
  $inst = $session.CreateUpdateInstaller(); $inst.Updates = $one
  try {
    $r = $inst.Install()
    $code = [int]$r.GetUpdateResult(0).ResultCode
    $txt = @{ 0 = 'not started'; 1 = 'in progress'; 2 = 'installed'; 3 = 'installed with errors'; 4 = 'failed'; 5 = 'aborted' }[$code]
    $results += [ordered]@{ title = $u.Title; result = $txt; reboot = [bool]$r.RebootRequired }
    Say ('   ' + $txt)
  } catch { $results += [ordered]@{ title = $u.Title; result = 'failed'; error = $_.Exception.Message }; Say ('   failed: ' + $_.Exception.Message) }
}
$sysInfo = New-Object -ComObject Microsoft.Update.SystemInfo
Out-Result @{ mode = 'install'; found = $list.Count
  installed = @($results | Where-Object { $_.result -like 'installed*' }).Count
  failed = @($results | Where-Object { $_.result -notlike 'installed*' }).Count
  rebootRequired = [bool]$sysInfo.RebootRequired; results = $results }
