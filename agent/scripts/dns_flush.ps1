Say 'Clearing the DNS cache...'; & "$env:windir\System32\ipconfig.exe" /flushdns | Out-Null  # not Clear-DnsClientCache: it goes through WMI, which hangs on PCs where WMI is damaged
Out-Result @{ flushed = $true }
