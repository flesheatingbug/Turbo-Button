$out = & dism.exe /Online /Cleanup-Image /RestoreHealth /English 2>&1 | ForEach-Object { "$_" }
$code = $LASTEXITCODE
$tail = (($out | Where-Object { $_.Trim() } | Select-Object -Last 4) -join ' | ')
if ($code -ne 0) { throw "DISM exit code $code : $tail" }
Out-Result @{ exitCode = $code; output = $tail }
