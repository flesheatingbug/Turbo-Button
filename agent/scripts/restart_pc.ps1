$sec = [int]$env:PCM_minutes * 60
& shutdown.exe /r /t $sec /c "Turbo Button: this PC will restart in $($env:PCM_minutes) minute(s) to finish the repairs. Please save your work." /d p:4:1
if ($LASTEXITCODE -ne 0) { throw "shutdown.exe failed ($LASTEXITCODE) - a restart may already be scheduled." }
Out-Result @{ restartInSeconds = $sec; note = "Restarting in $($env:PCM_minutes) min. The session ends when the PC restarts." }
