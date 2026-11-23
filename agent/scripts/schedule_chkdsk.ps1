$d = $env:PCM_drive.ToUpper()
$o = (& fsutil.exe dirty set "$($d):" 2>&1) -join ' '
if ($LASTEXITCODE -ne 0) { throw "fsutil failed: $o" }
Out-Result @{ drive = $d; scheduled = $true; rebootRequired = $true; note = 'Full disk check runs at the next restart (can take a while).' }
