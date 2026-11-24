# Removes one Microsoft Office install (2007-2016 MSI, 2013+/2019/2021/2024/Microsoft 365 Click-to-Run) silently.
$n = $env:PCM_name
$o = @(Get-OfficeInstalls | Where-Object { $_.name -eq $n }) | Select-Object -First 1
if (-not $o) { throw "'$n' isn't installed (the name must match the scan exactly). Rescan and try again." }
$e = $o.entry
Say "Removing $n ($($o.kind))"

$apps = 'WINWORD','EXCEL','POWERPNT','OUTLOOK','MSACCESS','MSPUB','ONENOTE','VISIO','WINPROJ','lync','GROOVE','MSOSYNC','OfficeClickToRun','OfficeC2RClient','integrator'
$open = @(Get-Process -Name $apps -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -notin 'OfficeClickToRun','integrator' })
if ($open) { Say ("Closing Office apps: " + (($open.ProcessName | Sort-Object -Unique) -join ', ')); $open | Stop-Process -Force -ErrorAction SilentlyContinue; Start-Sleep 2 }

$u = "$($e.UninstallString)"
$code = $null
switch ($o.kind) {
  'click-to-run' {
    $m = [regex]::Match($u, '^\s*"([^"]+OfficeClickToRun\.exe)"\s*(.*)$', 'IgnoreCase')
    if (-not $m.Success) { $m = [regex]::Match($u, '^\s*(\S+OfficeClickToRun\.exe)\s*(.*)$', 'IgnoreCase') }
    if (-not $m.Success) { throw "Couldn't read the Click-to-Run uninstall command: $u" }
    $a = ($m.Groups[2].Value -replace '(?i)\s*DisplayLevel=\S+', '') + ' DisplayLevel=False forceappshutdown=True'
    Say "Running Office's own uninstaller (Click-to-Run) silently. This takes 5-20 minutes."
    $p = Start-Process -FilePath $m.Groups[1].Value -ArgumentList $a -PassThru
  }
  'msi-setup' {
    $m = [regex]::Match($u, '(?i)^\s*"?(.+?setup\.exe)"?\s+/uninstall\s+(\S+)')
    $prod = $m.Groups[2].Value
    $cfg = Join-Path $env:TEMP "turbo-office-uninstall-$prod.xml"
    @"
<Configuration Product="$prod">
  <Display Level="none" CompletionNotice="no" SuppressModal="yes" AcceptEula="yes" />
  <Setting Id="SETUP_REBOOT" Value="Never" />
</Configuration>
"@ | Set-Content -Path $cfg -Encoding UTF8
    Say "Running Office setup to remove $prod silently. This takes 5-20 minutes."
    $p = Start-Process -FilePath $m.Groups[1].Value -ArgumentList '/uninstall', $prod, '/config', "`"$cfg`"" -PassThru
  }
  'msi' {
    Say ('Running: msiexec /x ' + $e.PSChildName + ' /qn')
    $p = Start-Process msiexec.exe -ArgumentList '/x', $e.PSChildName, '/qn', '/norestart' -PassThru
  }
  default { throw "'$n' doesn't have an uninstaller Turbo Button knows how to run silently. Use Microsoft's Office uninstall tool (Get Help app → 'uninstall Office')." }
}

# Wait for the uninstaller, then for the Programs entry to disappear (Click-to-Run hands off to a service).
$t0 = Get-Date; $last = $t0
while ($true) {
  $gone = -not @(Get-OfficeInstalls | Where-Object { $_.name -eq $n }).Count
  $running = $p -and -not $p.HasExited
  if ($gone -and -not $running) { break }
  if (((Get-Date) - $t0).TotalMinutes -gt 40) { break }
  if (((Get-Date) - $last).TotalSeconds -ge 60) { Say ("Still removing... {0:N0} min" -f ((Get-Date) - $t0).TotalMinutes); $last = Get-Date }
  Start-Sleep -Seconds 5
}
if ($p -and $p.HasExited) { $code = $p.ExitCode }
if ($cfg) { Remove-Item $cfg -Force -ErrorAction SilentlyContinue }
$still = @(Get-OfficeInstalls | Where-Object { $_.name -eq $n }).Count -gt 0
if ($still -and $code -notin 1641, 3010) {
  throw "Office's uninstaller finished (exit code $code) but '$n' is still listed. Restart and try again, or use Microsoft's Office uninstall tool (Get Help app → 'uninstall Office')."
}
$pst = @(Get-ChildItem "$env:USERPROFILE\Documents\Outlook Files", "$env:LOCALAPPDATA\Microsoft\Outlook" -Include *.pst, *.ost -Recurse -ErrorAction SilentlyContinue).Count
Out-Result @{ name = $n; kind = $o.kind; exitCode = $code; removed = (-not $still); rebootRequired = $true
  note = "Removed. Restart to finish. Outlook data files were left in place ($pst found). Reinstalling needs the customer's Microsoft account or product key." }
