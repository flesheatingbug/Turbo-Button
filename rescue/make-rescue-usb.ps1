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
param([string]$Iso)
$ErrorActionPreference = 'Stop'
$self = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole('Administrator')) { throw "Run this in an Administrator PowerShell." }
if (-not $Iso) {
  $Iso = Join-Path $self 'systemrescue-latest.iso'
  if ((Test-Path $Iso) -and (Get-Item $Iso).Length -gt 400MB) {
    Write-Host "Using the SystemRescue ISO already downloaded ($Iso)."
  } else {
    Write-Host "==> Downloading the latest SystemRescue ISO (about 900 MB, one time)"
    try { Invoke-WebRequest 'https://sourceforge.net/projects/systemrescuecd/files/latest/download' -OutFile "$Iso.part" -UseBasicParsing }
    catch { throw "couldn't download the ISO: $($_.Exception.Message). Download one from https://www.system-rescue.org/Download/ and pass it with -Iso" }
    if ((Get-Item "$Iso.part").Length -lt 400MB) { Remove-Item "$Iso.part"; throw "that download didn't look like an ISO. Try again, or pass one with -Iso" }
    Move-Item -Force "$Iso.part" $Iso
  }
}
if (-not (Test-Path $Iso)) { throw "ISO not found: $Iso" }
if (-not (Test-Path (Join-Path $self 'turbo-rescue.sh'))) { throw "missing turbo-rescue.sh next to this script" }
$agent = Join-Path $self 'turbobutton'
if (-not (Test-Path $agent)) {
  Write-Host "==> Downloading the Linux agent from GitHub"
  $rel = 'https://github.com/flesheatingbug/Turbo-Button/releases/download/latest'
  Invoke-WebRequest "$rel/TurboButton-linux-x86_64" -OutFile $agent
  $sums = (Invoke-WebRequest "$rel/SHA256SUMS" -UseBasicParsing).Content
  $want = (($sums -split "`n") | Where-Object { $_ -match 'TurboButton-linux-x86_64\s*$' }) -replace '\s.*', ''
  $got = (Get-FileHash $agent -Algorithm SHA256).Hash.ToLower()
  if ($want -and $want.Trim() -ne $got) { throw "downloaded agent checksum mismatch" }
}

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
