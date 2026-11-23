$k = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
$prev = Get-ItemProperty $k -ErrorAction SilentlyContinue
Set-ItemProperty $k -Name ProxyEnable -Value 0 -Type DWord
Remove-ItemProperty $k -Name ProxyServer, AutoConfigURL -ErrorAction SilentlyContinue
$w = (& netsh.exe winhttp reset proxy 2>&1) -join ' '
Out-Result @{ previousProxy = "$($prev.ProxyServer)"; previousAutoConfig = "$($prev.AutoConfigURL)"; previousEnabled = $prev.ProxyEnable; winhttp = $w.Trim()
  note = 'Restart open browsers.' }
