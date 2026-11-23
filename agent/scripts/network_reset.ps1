$out = @()
$out += (& netsh.exe winsock reset 2>&1)
$out += (& netsh.exe int ip reset 2>&1)
& ipconfig.exe /flushdns | Out-Null
foreach ($l in $out) { $t = "$l".Trim(); if ($t) { Say $t } }
Out-Result @{ rebootRequired = $true; note = 'Restart the PC to finish the network reset.' }
