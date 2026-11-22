$before = (Get-PSDrive ($env:SystemDrive.TrimEnd(':'))).Free
$out = & dism.exe /Online /Cleanup-Image /StartComponentCleanup /English 2>&1
$code = $LASTEXITCODE
$after = (Get-PSDrive ($env:SystemDrive.TrimEnd(':'))).Free
$tail = (($out | Select-Object -Last 4) -join ' ').Trim()
if ($code -ne 0) { throw "DISM exit code $code : $tail" }
Out-Result @{ freedBytes = [int64]($after - $before); exitCode = $code; output = $tail }
