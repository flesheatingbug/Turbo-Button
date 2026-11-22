$k = 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Power'
$prev = (Get-ItemProperty $k -ErrorAction SilentlyContinue).HiberbootEnabled
New-ItemProperty $k -Name HiberbootEnabled -Value ([int]$env:PCM_enabled) -PropertyType DWord -Force | Out-Null
Out-Result @{ previous = $prev; fastStartup = [int]$env:PCM_enabled }
