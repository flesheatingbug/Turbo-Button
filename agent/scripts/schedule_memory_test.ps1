$o = (& bcdedit.exe /bootsequence '{memdiag}' 2>&1) -join ' '
if ($LASTEXITCODE -ne 0) { throw "bcdedit failed: $o" }
Out-Result @{ scheduled = $true; rebootRequired = $true; note = 'Memory test runs at the next restart (10-20 min). Rescan afterwards for the result.' }
