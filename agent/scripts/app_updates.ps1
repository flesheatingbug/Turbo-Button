$wg = Get-Winget
if (-not $wg) { throw 'winget (App Installer) is not available on this PC. Install "App Installer" from the Microsoft Store first.' }
$lines = @()
if ($env:PCM_mode -eq 'install') {
  Say 'Updating all programs that have updates (silent)...'
  & $wg upgrade --all --silent --accept-package-agreements --accept-source-agreements --disable-interactivity 2>&1 | ForEach-Object {
    $t = Clean-Line $_; if ($t) { Say $t; $lines += $t } }
  $ok = @($lines | Where-Object { $_ -match 'Successfully installed' }).Count
  Out-Result @{ mode = 'install'; installed = $ok; exitCode = $LASTEXITCODE; output = (($lines | Select-Object -Last 40) -join "`n") }
} else {
  & $wg upgrade --accept-source-agreements --disable-interactivity 2>&1 | ForEach-Object { $t = Clean-Line $_; if ($t) { Say $t; $lines += $t } }
  $count = 0; $m = $lines | Select-String -Pattern '^(\d+) upgrades? available' | Select-Object -First 1
  if ($m) { $count = [int]$m.Matches[0].Groups[1].Value }
  Out-Result @{ mode = 'list'; available = $count; output = (($lines | Select-Object -Last 60) -join "`n") }
}
