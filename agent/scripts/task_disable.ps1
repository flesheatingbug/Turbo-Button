$t = Get-ScheduledTask -TaskPath $env:PCM_path -TaskName $env:PCM_name -ErrorAction Stop
if ($t.TaskPath -like '\Microsoft\*') { throw 'Microsoft tasks are not changed by this tool' }
$t | Disable-ScheduledTask -ErrorAction Stop | Out-Null
Out-Result @{ path = $t.TaskPath; name = $t.TaskName; previousState = "$($t.State)"; state = 'Disabled' }
