$d = $env:PCM_drive.ToUpper()
$out = Optimize-Volume -DriveLetter $d -Verbose 4>&1 -ErrorAction Stop | ForEach-Object { "$_" }
Out-Result @{ drive = $d; output = (($out | Select-Object -Last 6) -join ' | ') }
