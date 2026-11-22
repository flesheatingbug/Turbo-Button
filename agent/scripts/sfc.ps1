try { [Console]::OutputEncoding = [Text.Encoding]::Unicode } catch {}
$out = & sfc.exe /scannow
$code = $LASTEXITCODE
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch {}
$text = (($out | ForEach-Object { "$_" -replace [char]0, '' }) -join ' ') -replace '\s+', ' '
$verdict = if ($text -match 'did not find any integrity violations') { 'clean' }
  elseif ($text -match 'successfully repaired') { 'repaired' }
  elseif ($text -match 'unable to fix') { 'unrepaired - run dism_restore then sfc again' }
  elseif ($text -match 'could not perform') { 'could not run - pending reboot or servicing issue' }
  else { 'unknown (see output)' }
$tail = if ($text.Length -gt 400) { $text.Substring($text.Length - 400) } else { $text }
Out-Result @{ verdict = $verdict; exitCode = $code; output = $tail }
