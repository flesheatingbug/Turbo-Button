$changes = @()
foreach ($a in @(Get-NetAdapter -ErrorAction Stop | Where-Object Status -eq 'Up')) {
  $before = (Get-DnsClientServerAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue).ServerAddresses -join ', '
  Set-DnsClientServerAddress -InterfaceIndex $a.ifIndex -ResetServerAddresses -ErrorAction Stop
  $after = (Get-DnsClientServerAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue).ServerAddresses -join ', '
  $changes += "$($a.Name): $before -> $after (automatic)"
  Say $changes[-1]
}
Clear-DnsClientCache
Out-Result @{ changes = $changes }
