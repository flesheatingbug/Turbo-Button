$n = $env:PCM_name
$e = @(Get-ItemProperty $UninstallKeys -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $n }) | Select-Object -First 1
if (-not $e) { throw "Program '$n' is not installed (name must match the scan exactly)." }
$how = $null; $code = $null
if ($e.QuietUninstallString) {
  $how = 'quiet uninstaller'
  Say ('Running: ' + $e.QuietUninstallString)
  $p = Start-Process -FilePath $env:ComSpec -ArgumentList '/s', '/c', ('"' + $e.QuietUninstallString + '"') -Wait -PassThru -WindowStyle Hidden
  $code = $p.ExitCode
} elseif ("$($e.UninstallString)" -match 'msiexec' -and $e.PSChildName -match '^\{[0-9A-Fa-f\-]+\}$') {
  $how = 'Windows Installer'
  Say ('Running: msiexec /x ' + $e.PSChildName + ' /qn')
  $p = Start-Process msiexec.exe -ArgumentList '/x', $e.PSChildName, '/qn', '/norestart' -Wait -PassThru
  $code = $p.ExitCode
} else {
  $wg = Get-Winget
  if (-not $wg) { throw "'$n' has no silent uninstaller and winget isn't available. Remove it by hand (Settings > Apps) or with the vendor's removal tool." }
  $how = 'winget'
  Say 'No silent uninstaller registered - trying winget...'
  & $wg uninstall --name $n --exact --silent --accept-source-agreements --disable-interactivity 2>&1 | ForEach-Object { $t = Clean-Line $_; if ($t) { Say $t } }
  $code = $LASTEXITCODE
}
Start-Sleep -Seconds 3
$still = @(Get-ItemProperty $UninstallKeys -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $n }).Count -gt 0
if ($still -and $code -notin 1641, 3010) {
  throw "Uninstaller finished ($how, exit code $code) but '$n' is still installed. Remove it by hand, or with the vendor's removal tool (e.g. McAfee MCPR, Norton Remove and Reinstall)."
}
Out-Result @{ name = $n; method = $how; exitCode = $code; removed = (-not $still); rebootRequired = ($code -in 1641, 3010)
  note = if ($code -in 1641, 3010) { 'Removal finishes after a restart.' } else { '' } }
