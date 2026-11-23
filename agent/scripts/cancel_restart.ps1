& shutdown.exe /a 2>&1 | Out-Null
Out-Result @{ cancelled = ($LASTEXITCODE -eq 0); note = if ($LASTEXITCODE -eq 0) { 'Restart cancelled.' } else { 'No restart was pending.' } }
