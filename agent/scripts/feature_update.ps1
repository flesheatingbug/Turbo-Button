# Windows 11 version upgrade (e.g. 25H2 -> 26H2).
# check: read-only, compares this PC's version with the newest yearly release.
# install: briefly pins Windows Update to the target version so it offers it (the small
# "enablement package" on 24H2/25H2), installs it and removes the pin again. If Windows Update
# still won't offer it and PCM_assistant=1, runs Microsoft's Windows 11 Installation Assistant
# (signature checked) for a full in-place upgrade. Never touches safeguard holds.
# PCM_bypass=1 (technician's call, for a PC that fails Microsoft's TPM/Secure Boot/CPU check):
# sets the documented override keys and, if PCM_iso points to a Windows 11 ISO, does an in-place
# upgrade from it with the hardware check skipped, keeping files and programs.
if (Clear-TurboPin) { Say 'Removed a version pin left by an earlier run that was cut short.' }
$cv = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
$build = [int]$cv.CurrentBuild
$isWin11 = $build -ge 22000
$current = "$($cv.DisplayVersion)"
function VerNum($v) { if ($v -match '^(\d\d)H([12])$') { return [int]$matches[1] * 2 + [int]$matches[2] } ; return 0 }
$now = Get-Date
$target = if ($env:PCM_version -match '^\d\dH[12]$') { $env:PCM_version } else { '{0}H2' -f (($(if ($now.Month -ge 10) { $now.Year } else { $now.Year - 1 })) % 100) }
$upToDate = $isWin11 -and (VerNum $current) -ge (VerNum $target)
$pol = Get-ItemProperty $WUPolicyKey -ErrorAction SilentlyContinue
$managed = [bool]($pol -and ($pol.WUServer -or $pol.TargetReleaseVersion -or $pol.DisableOSUpgrade))
$osName = if ($isWin11) { 'Windows 11' } else { 'Windows 10' }
Say "This PC: $osName $current (build $build). Newest release: Windows 11 $target."
$base = @{ mode = $env:PCM_mode; current = $current; build = $build; os = $osName; target = $target; upToDate = $upToDate; managed = $managed }
if ($upToDate) { Say 'Already on the newest version.'; Out-Result ($base + @{ result = 'up to date' }); return }
if ($managed) {
  Say 'Windows Update on this PC is managed by policy (WSUS, Intune or a pinned version), so the version is left alone. Ask whoever manages it.'
  Out-Result ($base + @{ result = 'managed' }); return
}
if ($env:PCM_mode -ne 'install') { Out-Result ($base + @{ result = 'upgrade available' }); return }

$bypass = $env:PCM_bypass -eq '1'
if ($bypass) {
  Say 'Setting the documented overrides so Setup allows this PC despite the Windows 11 hardware requirements.'
  New-Item 'HKLM:\SYSTEM\Setup\MoSetup' -Force | Out-Null
  New-ItemProperty 'HKLM:\SYSTEM\Setup\MoSetup' -Name AllowUpgradesWithUnsupportedTPMOrCPU -Value 1 -PropertyType DWord -Force | Out-Null
  New-Item 'HKLM:\SYSTEM\Setup\LabConfig' -Force | Out-Null
  foreach ($n in 'BypassTPMCheck', 'BypassSecureBootCheck', 'BypassCPUCheck', 'BypassRAMCheck', 'BypassStorageCheck') {
    New-ItemProperty 'HKLM:\SYSTEM\Setup\LabConfig' -Name $n -Value 1 -PropertyType DWord -Force | Out-Null
  }
  $iso = "$env:PCM_iso"
  if ($iso) {
    if (-not (Test-Path -LiteralPath $iso)) { throw "Can't find the ISO at $iso." }
    Say "Mounting $iso ..."
    $before = (Get-Volume).DriveLetter
    $img = Mount-DiskImage -ImagePath $iso -PassThru -ErrorAction Stop
    Start-Sleep 3
    $drive = ($img | Get-DiskImage | Get-Volume).DriveLetter
    if (-not $drive) { $drive = ((Get-Volume).DriveLetter | Where-Object { $_ -and $_ -notin $before }) | Select-Object -First 1 }
    try {
      $setup = "${drive}:\setup.exe"
      if (-not (Test-Path $setup)) { throw "No setup.exe on the mounted ISO ($setup). Is it a Windows 11 ISO?" }
      $sig = Get-AuthenticodeSignature $setup
      if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw "setup.exe on the ISO isn't signed by Microsoft ($($sig.Status)); not run." }
      $logDir = Join-Path $env:TEMP 'turbo-win11'; New-Item -ItemType Directory -Force $logDir | Out-Null
      Say 'Starting the in-place upgrade from the ISO (hardware check skipped via /product server). Files and programs are kept. This takes a while.'
      # /product server makes Setup skip the Windows 11 client hardware appraisal while still doing a client upgrade.
      $p = Start-Process $setup -ArgumentList '/auto', 'upgrade', '/product', 'server', '/dynamicupdate', 'disable', '/eula', 'accept', '/noreboot', '/copylogs', $logDir -PassThru -Wait
      $t0 = Get-Date
      while (Get-Process -Name SetupHost, SetupPrep -ErrorAction SilentlyContinue) {
        if ([int]((Get-Date) - $t0).TotalSeconds % 300 -lt 15) { Say ('Still upgrading... ' + [int]((Get-Date) - $t0).TotalMinutes + ' min') }
        Start-Sleep 15
      }
      $pending = (Test-Path "$env:SystemDrive\`$WINDOWS.~BT\Sources\SetupPlatform.exe") -or (Test-Path 'HKLM:\SYSTEM\Setup\MoSetup\Volatile')
      $installed = $pending
      if ($installed) { Say "Upgrade staged from the ISO. Windows 11 $target finishes during the next restart (20-40 min)." }
      else { Say "Setup exited (code $($p.ExitCode)) without staging. Check $logDir\\*.log (setupact.log)." }
    } finally { Dismount-DiskImage -ImagePath $iso -ErrorAction SilentlyContinue | Out-Null }
    Out-Result ($base + @{ result = $(if ($installed) { 'installed' } else { 'failed' }); method = 'ISO (/product server)'; bypass = $true; rebootRequired = $true })
    return
  }
  Say 'No ISO given, so trying the Installation Assistant with the overrides. This works when the PC has TPM 1.2 and Secure Boot but an unsupported CPU. A PC with no TPM or no Secure Boot needs an ISO (point the step at a Windows 11 ISO, e.g. on your Medicat USB).'
  $env:PCM_assistant = '1'
}

function Find-Feature($session) {
  $res = $session.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
  foreach ($u in $res.Updates) {
    $cats = @($u.Categories | ForEach-Object { $_.Name })
    if ($u.Title -match 'Feature update to Windows 11|Windows 11, version \d\dH\d' -or $cats -contains 'Upgrades') { return $u }
  }
  return $null
}
$session = New-Object -ComObject Microsoft.Update.Session
$session.ClientApplicationID = 'Turbo Button'
$feature = $null; $installed = $false; $reboot = $false; $how = $null
try {
  if (-not (Test-Path $WUPolicyKey)) { New-Item $WUPolicyKey -Force | Out-Null }
  New-ItemProperty $WUPolicyKey -Name TurboButtonPin -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty $WUPolicyKey -Name TargetReleaseVersion -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty $WUPolicyKey -Name ProductVersion -Value 'Windows 11' -PropertyType String -Force | Out-Null
  New-ItemProperty $WUPolicyKey -Name TargetReleaseVersionInfo -Value $target -PropertyType String -Force | Out-Null
  Say "Asking Windows Update for Windows 11 $target (this can take a few minutes)..."
  $feature = Find-Feature $session
  if ($feature) {
    Say ('Found: ' + $feature.Title)
    if (-not $feature.EulaAccepted) { $feature.AcceptEula() }
    $one = New-Object -ComObject Microsoft.Update.UpdateColl; [void]$one.Add($feature)
    Say 'Downloading...'
    $dl = $session.CreateUpdateDownloader(); $dl.Updates = $one; [void]$dl.Download()
    Say 'Installing...'
    $inst = $session.CreateUpdateInstaller(); $inst.Updates = $one
    $r = $inst.Install(); $code = [int]$r.GetUpdateResult(0).ResultCode
    $installed = $code -in 2, 3; $reboot = [bool]$r.RebootRequired; $how = 'Windows Update'
    Say $(if ($installed) { "Installed. Windows 11 $target takes effect after a restart." } else { "Windows Update couldn't install it (result $code)." })
  } else {
    Say "Windows Update isn't offering Windows 11 $target to this PC yet. Usually that means a newer monthly update must go in first (run Windows Update), Microsoft is holding it back for a known problem with this hardware, or the PC doesn't meet Windows 11's requirements."
  }
} finally { [void](Clear-TurboPin) }

if (-not $installed -and $env:PCM_assistant -eq '1') {
  $dir = Join-Path $env:TEMP 'turbo-win11'; New-Item -ItemType Directory -Force $dir | Out-Null
  $exe = Join-Path $dir 'Windows11InstallationAssistant.exe'
  Say "Downloading Microsoft's Windows 11 Installation Assistant..."
  try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch {}
  Invoke-WebRequest -UseBasicParsing 'https://go.microsoft.com/fwlink/?linkid=2171764' -OutFile $exe
  $sig = Get-AuthenticodeSignature $exe
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') {
    Remove-Item $exe -Force -ErrorAction SilentlyContinue
    throw "The downloaded Installation Assistant isn't signed by Microsoft ($($sig.Status)), so it wasn't run."
  }
  Say 'Running the Installation Assistant quietly. This is a full upgrade: it downloads several GB and can take an hour or more. Files and programs are kept.'
  $p = Start-Process $exe -ArgumentList '/QuietInstall', '/SkipEULA', '/Auto', 'Upgrade', '/NoRestartUI', '/CopyLogs', $dir -PassThru
  $p.WaitForExit()
  # The assistant hands over to its upgrader, which keeps running after it exits.
  $t0 = Get-Date
  while (Get-Process -Name Windows10UpgraderApp, SetupHost -ErrorAction SilentlyContinue) {
    if (((Get-Date) - $t0).TotalMinutes -ge 1 -and [int]((Get-Date) - $t0).TotalSeconds % 300 -lt 15) { Say ('Still upgrading... ' + [int]((Get-Date) - $t0).TotalMinutes + ' min') }
    Start-Sleep 15
  }
  $how = 'Installation Assistant'; $reboot = $true
  $pending = (Test-Path "$env:SystemDrive\`$WINDOWS.~BT") -or (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending')
  $installed = $pending -and $p.ExitCode -eq 0
  if ($installed) { Say "The upgrade is staged. Windows 11 $target finishes installing during the next restart (allow 20-40 minutes)." }
  else { Say "The Installation Assistant stopped (exit code $($p.ExitCode)) without staging the upgrade. Its logs are in $dir. The most common reason is that the PC doesn't meet Windows 11's requirements (TPM 2.0, Secure Boot, supported CPU)." }
}
Out-Result ($base + @{ result = $(if ($installed) { 'installed' } elseif ($feature) { 'failed' } else { 'not offered' }); method = $how; bypass = $bypass; rebootRequired = $reboot })
