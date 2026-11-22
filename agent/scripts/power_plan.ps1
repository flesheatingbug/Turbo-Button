$guid = @{ balanced = '381b4222-f694-41f0-9685-ff5bb260df2e'; high_performance = '8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c' }[$env:PCM_plan]
$prev = ((powercfg /getactivescheme) -join ' ').Trim()
$list = (powercfg /list) -join ' '
if ($list -notmatch $guid) { powercfg /duplicatescheme $guid $guid | Out-Null }
powercfg /setactive $guid
if ($LASTEXITCODE -ne 0) { throw "powercfg failed ($LASTEXITCODE). This PC may only support the Balanced plan (Modern Standby)." }
Out-Result @{ previous = $prev; active = ((powercfg /getactivescheme) -join ' ').Trim() }
