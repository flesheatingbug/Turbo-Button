Say 'Scanning the Windows component store (read-only, 5-15 min)...'
$out = & dism.exe /Online /Cleanup-Image /ScanHealth /English 2>&1 | ForEach-Object { "$_" }
$code = $LASTEXITCODE
$text = ($out | Where-Object { $_.Trim() }) -join ' '
$verdict = if ($text -match 'No component store corruption detected') { 'clean' }
  elseif ($text -match 'repairable') { 'corruption found (repairable)' }
  elseif ($text -match 'not repairable') { 'corruption found (not repairable)' }
  elseif ($code -eq 0) { 'clean' } else { 'unknown (see output)' }
Out-Result @{ verdict = $verdict; corrupt = ($verdict -like 'corruption*'); exitCode = $code; output = (($out | Where-Object { $_.Trim() } | Select-Object -Last 3) -join ' | ') }
