$svc = Get-Service WSearch -ErrorAction Stop
if ($svc.StartType -eq 'Disabled') { throw 'Windows Search service is disabled - enable it first (service_mode WSearch Automatic).' }
Stop-Service WSearch -Force -ErrorAction Stop
Set-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows Search' -Name SetupCompletedSuccessfully -Value 0 -Type DWord
Start-Service WSearch -ErrorAction Stop
Out-Result @{ rebuilding = $true; note = 'The index rebuilds in the background over the next hours.' }
