$p = Get-AppxPackage -AllUsers -Name Microsoft.WindowsStore -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $p) { throw 'Microsoft Store is not installed on this PC.' }
Say ('Re-registering ' + $p.PackageFullName)
Add-AppxPackage -DisableDevelopmentMode -Register (Join-Path $p.InstallLocation 'AppXManifest.xml') -ErrorAction Stop
Out-Result @{ reRegistered = $p.PackageFullName; note = 'If the Store still misbehaves, run wsreset from the Start menu.' }
