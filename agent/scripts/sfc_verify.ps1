Say 'Checking system files (read-only, 5-20 min)...'
try { [Console]::OutputEncoding = [Text.Encoding]::Unicode } catch {}
$out = & sfc.exe /verifyonly
$code = $LASTEXITCODE
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch {}
$text = (($out | ForEach-Object { "$_" -replace [char]0, '' }) -join ' ') -replace '\s+', ' '
$verdict = if ($text -match 'did not find any integrity violations') { 'clean' }
  elseif ($text -match 'found integrity violations') { 'corruption found' }
  elseif ($text -match 'could not perform') { 'could not run' }
  elseif ($code -eq 0) { 'clean' } else { 'unknown (see output)' }
$tail = if ($text.Length -gt 300) { $text.Substring($text.Length - 300) } else { $text }
Out-Result @{ verdict = $verdict; corrupt = ($verdict -eq 'corruption found'); exitCode = $code; output = $tail }
