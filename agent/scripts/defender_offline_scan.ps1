Say 'The PC will restart into Microsoft Defender Offline scan now (about 15 minutes).'
Out-Result @{ starting = $true; note = 'Restarting into offline scan - the session ends now.' }
Start-Sleep -Seconds 3
Start-MpWDOScan
