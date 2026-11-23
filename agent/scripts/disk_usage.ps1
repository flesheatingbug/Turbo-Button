function Measure-Dir([string]$p) {
  if (-not (Test-Path -LiteralPath $p)) { return $null }
  $out = & robocopy.exe $p "$env:TEMP\tb-null-$PID" /L /S /NJH /BYTES /NFL /NDL /NC /NP /XJ /R:0 /W:0 2>$null
  $rows = @($out | Where-Object { $_ -match '^\s*[^:]+:\s+\d+\s+\d+' })
  if ($rows.Count -ge 3) { $n = @($rows[2] -split '\s+' | Where-Object { $_ -match '^\d+$' })[0]; if ($n) { return [int64]$n } }
  return $null
}
$targets = @()
foreach ($u in @(Get-ChildItem 'C:\Users' -Directory -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -notin 'Public', 'Default', 'Default User', 'All Users' })) {
  foreach ($sub in 'Desktop', 'Documents', 'Downloads', 'Pictures', 'Videos', 'Music', 'OneDrive', 'AppData\Local', 'AppData\Roaming') {
    $targets += Join-Path $u.FullName $sub
  }
}
$targets += "$env:windir", "$env:ProgramFiles", "${env:ProgramFiles(x86)}", "$env:ProgramData"
$std = 'Windows', 'Program Files', 'Program Files (x86)', 'ProgramData', 'Users', 'PerfLogs', 'Recovery', '$Recycle.Bin', 'System Volume Information', 'Config.Msi', 'Documents and Settings', '$WinREAgent', 'OneDriveTemp'
$targets += @(Get-ChildItem 'C:\' -Directory -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -notin $std } | ForEach-Object { $_.FullName })
$res = @()
foreach ($t in $targets) {
  if (-not (Test-Path -LiteralPath $t)) { continue }
  Say "Measuring $t"
  $b = Measure-Dir $t
  if ($null -ne $b) { $res += [ordered]@{ path = $t; gb = [math]::Round($b / 1GB, 2) } }
}
$top = @($res | Sort-Object { $_.gb } -Descending | Select-Object -First 25)
foreach ($r in $top | Select-Object -First 8) { Say ("{0,8} GB  {1}" -f $r.gb, $r.path) }
Out-Result @{ folders = $top; note = 'Largest: ' + (($top | Select-Object -First 3 | ForEach-Object { "$($_.path) $($_.gb) GB" }) -join '; ') }
