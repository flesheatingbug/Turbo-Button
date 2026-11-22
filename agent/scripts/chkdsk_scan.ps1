$d = $env:PCM_drive.ToUpper()
$out = & chkdsk.exe "$($d):" /scan 2>&1 | ForEach-Object { "$_" }
$code = $LASTEXITCODE
Out-Result @{ drive = $d; exitCode = $code; problemsFound = ($code -ne 0); output = (($out | Where-Object { $_.Trim() } | Select-Object -Last 8) -join ' | ') }
