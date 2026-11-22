Enable-ComputerRestore -Drive "$env:SystemDrive\" -ErrorAction SilentlyContinue
$k = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\SystemRestore'
$old = (Get-ItemProperty $k -ErrorAction SilentlyContinue).SystemRestorePointCreationFrequency
New-ItemProperty $k -Name SystemRestorePointCreationFrequency -Value 0 -PropertyType DWord -Force | Out-Null
try {
  Checkpoint-Computer -Description 'Turbo Button - before changes' -RestorePointType MODIFY_SETTINGS -ErrorAction Stop
} finally {
  if ($null -eq $old) { Remove-ItemProperty $k -Name SystemRestorePointCreationFrequency -ErrorAction SilentlyContinue }
  else { Set-ItemProperty $k -Name SystemRestorePointCreationFrequency -Value $old }
}
$rp = @(Get-ComputerRestorePoint)[-1]
Out-Result @{ created = $true; description = $rp.Description; time = "$($rp.CreationTime)" }
