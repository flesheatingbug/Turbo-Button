$sec = [int]$env:PCM_minutes * 60
& shutdown.exe /s /t $sec /c "Turbo Button: this PC will shut down in $($env:PCM_minutes) minute(s). Please save your work." /d p:4:1
if ($LASTEXITCODE -ne 0) { throw "shutdown.exe failed ($LASTEXITCODE) - a shutdown or restart may already be scheduled (cancel it first)." }
Out-Result @{ shutdownInSeconds = $sec; note = "Shutting down in $($env:PCM_minutes) min. The session ends when the PC powers off." }
