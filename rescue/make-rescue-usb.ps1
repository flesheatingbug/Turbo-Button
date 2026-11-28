<#  Make a standalone Turbo Button rescue USB from a SystemRescue ISO (UEFI boot).
    Run in an ADMIN PowerShell:
        .\make-rescue-usb.ps1 -Iso C:\path\systemrescue.iso
    It lists your removable drives and asks which to use, formats it FAT32, copies SystemRescue
    (UEFI-bootable) and Turbo Button onto it. WIPES the chosen drive.
    Get the ISO from https://www.system-rescue.org . 'turbobutton' (the Linux agent),
    'turbobutton.conf' and 'turbo-rescue.sh' must sit next to this script.
    NOTE: a USB made on Windows this way boots via UEFI (any PC from ~2015 on). For an old
    BIOS-only PC, make the stick with the Linux script, or write the ISO with Rufus/Etcher
    and copy the turbo-rescue folder on afterwards.                                        #>
param([Parameter(Mandatory=$true)][string]$Iso)
$ErrorActionPreference = 'Stop'
$self = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole('Administrator')) { throw "Run this in an Administrator PowerShell." }
if (-not (Test-Path $Iso)) { throw "ISO not found: $Iso" }
foreach ($f in 'turbobutton','turbo-rescue.sh') { if (-not (Test-Path (Join-Path $self $f))) { throw "missing $f next to this script" } }

Write-Host "Removable drives:`n"
$disks = Get-Disk | Where-Object { $_.BusType -eq 'USB' }
if (-not $disks) { throw "No USB drive found. Plug one in." }
$disks | Format-Table Number, @{n='Size(GB)';e={[math]::Round($_.Size/1GB)}}, FriendlyName -AutoSize
$num = Read-Host "Enter the disk NUMBER to erase"
$disk = Get-Disk -Number $num
if ($disk.BusType -ne 'USB') { throw "Disk $num isn't a USB drive — refusing." }
if ($disk.Size -gt 128GB) { throw "Disk $num is $([math]::Round($disk.Size/1GB)) GB — too large for a USB stick; refusing in case it's a hard drive." }
Write-Host "About to ERASE disk $num ($($disk.FriendlyName), $([math]::Round($disk.Size/1GB)) GB)." -ForegroundColor Yellow
if ((Read-Host "Type ERASE to continue") -ne 'ERASE') { throw "cancelled" }

# Read the ISO's volume label (SystemRescue finds its files by this label when UEFI-booting).
$mount = Mount-DiskImage -ImagePath $Iso -PassThru
Start-Sleep 2
$isoVol = ($mount | Get-Volume)
$label = if ($isoVol.FileSystemLabel) { $isoVol.FileSystemLabel } else { 'RESCUE' }
$isoDrive = $isoVol.DriveLetter
try {
  Write-Host "==> Partitioning and formatting disk $num (FAT32, label $label)"
  Clear-Disk -Number $num -RemoveData -RemoveOEM -Confirm:$false
  Initialize-Disk -Number $num -PartitionStyle GPT -Confirm:$false
  $part = New-Partition -DiskNumber $num -UseMaximumSize -AssignDriveLetter
  # FAT32 label max 11 chars.
  $lab = $label.Substring(0, [Math]::Min(11, $label.Length))
  Format-Volume -Partition $part -FileSystem FAT32 -NewFileSystemLabel $lab -Confirm:$false | Out-Null
  $usb = "$($part.DriveLetter):"
  Write-Host "==> Copying SystemRescue (UEFI) to $usb"
  Copy-Item -Path "$isoDrive`:\*" -Destination "$usb\" -Recurse -Force
  Write-Host "==> Copying Turbo Button"
  New-Item -ItemType Directory -Force "$usb\turbo-rescue" | Out-Null
  Copy-Item (Join-Path $self 'turbobutton') "$usb\turbo-rescue\" -Force
  if (Test-Path (Join-Path $self 'turbobutton.conf')) { Copy-Item (Join-Path $self 'turbobutton.conf') "$usb\turbo-rescue\" -Force }
  Copy-Item (Join-Path $self 'turbo-rescue.sh') "$usb\turbo-rescue\" -Force
  "#!/bin/sh`nexec sh `"`$(dirname `"`$0`")/turbo-rescue/turbo-rescue.sh`"`n" | Set-Content -NoNewline -Encoding Ascii "$usb\autorun"
} finally {
  Dismount-DiskImage -ImagePath $Iso | Out-Null
}
Write-Host "`nDone. Boot the dead PC from the USB (UEFI), connect WiFi, and run Turbo Button:" -ForegroundColor Green
Write-Host "  pick the 'autoruns' boot entry, or mount the stick and run turbo-rescue/turbo-rescue.sh"
