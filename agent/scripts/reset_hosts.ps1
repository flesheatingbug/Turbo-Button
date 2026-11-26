$h = "$env:windir\System32\drivers\etc\hosts"
$bk = Join-Path (Backup-Dir) ('hosts-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
Say 'Backing up the hosts file...'
Copy-Item $h $bk -Force -ErrorAction Stop
$removed = @(Get-Content $h -ErrorAction SilentlyContinue | Where-Object { $_.Trim() -and -not $_.Trim().StartsWith('#') })
$default = "# Copyright (c) 1993-2009 Microsoft Corp.`r`n#`r`n# This is a sample HOSTS file used by Microsoft TCP/IP for Windows.`r`n#`r`n# localhost name resolution is handled within DNS itself.`r`n#`t127.0.0.1       localhost`r`n#`t::1             localhost`r`n"
Say 'Writing the default hosts file (if antivirus on the PC asks, allow it)...'
[IO.File]::WriteAllText($h, $default, [Text.Encoding]::ASCII)
Say 'Clearing the DNS cache...'; & "$env:windir\System32\ipconfig.exe" /flushdns | Out-Null  # not Clear-DnsClientCache: it goes through WMI, which hangs on PCs where WMI is damaged
Out-Result @{ backup = $bk; removedEntries = $removed.Count; removed = @($removed | Select-Object -First 30) }
