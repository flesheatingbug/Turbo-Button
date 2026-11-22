$r = [ordered]@{ scannedAt = (Get-Date).ToString('s'); errors = @() }
function Section($name, [scriptblock]$sb) {
  Say "Scanning: $name"
  try { $r[$name] = & $sb } catch { $r.errors += "$name : $($_.Exception.Message)" }
}
function Short($s, $n) { $t = ("$s" -replace '\s+', ' ').Trim(); if ($t.Length -gt $n) { $t.Substring(0, $n) + '...' } else { $t } }
function GB($b) { [math]::Round(([double]$b) / 1GB, 2) }

Section 'system' {
  $os = Get-CimInstance Win32_OperatingSystem
  $cs = Get-CimInstance Win32_ComputerSystem
  $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
  $cv = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
  $bios = Get-CimInstance Win32_BIOS
  [ordered]@{
    computerName = $env:COMPUTERNAME; os = $os.Caption; displayVersion = $cv.DisplayVersion
    build = "$($cv.CurrentBuild).$($cv.UBR)"; edition = $cv.EditionID
    installDate = $os.InstallDate.ToString('yyyy-MM-dd'); lastBoot = $os.LastBootUpTime.ToString('s')
    uptimeHours = [math]::Round(((Get-Date) - $os.LastBootUpTime).TotalHours, 1)
    manufacturer = $cs.Manufacturer; model = $cs.Model; bios = "$($bios.SMBIOSBIOSVersion) ($($bios.ReleaseDate))"
    cpu = "$($cpu.Name)".Trim(); cores = $cpu.NumberOfCores; threads = $cpu.NumberOfLogicalProcessors
    cpuLoadPct = $cpu.LoadPercentage
    ramGB = GB $cs.TotalPhysicalMemory; freeRamGB = [math]::Round($os.FreePhysicalMemory / 1MB, 2)
    isLaptop = [bool](Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue)
    signedInUser = $cs.UserName; agentRunningAs = "$env:USERDOMAIN\$env:USERNAME"
  }
}

Section 'gpu' {
  @(Get-CimInstance Win32_VideoController | ForEach-Object {
    [ordered]@{ name = $_.Name; driverVersion = $_.DriverVersion; driverDate = if ($_.DriverDate) { $_.DriverDate.ToString('yyyy-MM-dd') } else { $null }; status = $_.Status }
  })
}

Section 'volumes' {
  @(Get-Volume | Where-Object { $_.DriveLetter -and $_.Size -gt 0 } | ForEach-Object {
    [ordered]@{ drive = "$($_.DriveLetter)"; label = $_.FileSystemLabel; fs = $_.FileSystem; sizeGB = GB $_.Size; freeGB = GB $_.SizeRemaining
      freePct = [math]::Round(100 * $_.SizeRemaining / $_.Size, 1); health = "$($_.HealthStatus)"; type = "$($_.DriveType)" }
  })
}

Section 'physicalDisks' {
  @(Get-PhysicalDisk | ForEach-Object {
    $rc = $_ | Get-StorageReliabilityCounter -ErrorAction SilentlyContinue
    [ordered]@{ name = $_.FriendlyName; mediaType = "$($_.MediaType)"; bus = "$($_.BusType)"; sizeGB = GB $_.Size
      health = "$($_.HealthStatus)"; operational = ($_.OperationalStatus -join ',')
      wearPct = $rc.Wear; tempC = $rc.Temperature; readErrors = $rc.ReadErrorsTotal; writeErrors = $rc.WriteErrorsTotal; powerOnHours = $rc.PowerOnHours }
  })
}

Section 'cleanupPotentialGB' {
  $o = [ordered]@{}
  foreach ($k in $CleanupTargets.Keys) { if ($k -ne 'recycle_bin') { $o[$k] = GB (Get-PathSize $CleanupTargets[$k]) } }
  $o['recycle_bin'] = GB (Get-RecycleSize)
  $o['windowsOldExists'] = Test-Path "$env:SystemDrive\Windows.old"
  $o['hiberfilGB'] = GB ((Get-Item "$env:SystemDrive\hiberfil.sys" -Force -ErrorAction SilentlyContinue).Length)
  $o['pagefileGB'] = GB ((Get-Item "$env:SystemDrive\pagefile.sys" -Force -ErrorAction SilentlyContinue).Length)
  $o
}

Section 'startup' {
  function Approved($root, $sub) {
    $h = @{}; $p = "$root\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\$sub"
    if (Test-Path $p) { $k = Get-Item $p; foreach ($n in $k.GetValueNames()) { $v = $k.GetValue($n); if ($v -is [byte[]] -and $v.Length -gt 0) { $h[$n] = (($v[0] % 2) -eq 0) } } }
    $h
  }
  $items = @()
  $regs = @(
    @{ loc = 'HKCU_Run'; key = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'; root = 'HKCU:'; sub = 'Run' },
    @{ loc = 'HKLM_Run'; key = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'; root = 'HKLM:'; sub = 'Run' },
    @{ loc = 'HKLM_Run32'; key = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Run'; root = 'HKLM:'; sub = 'Run32' })
  foreach ($g in $regs) {
    if (-not (Test-Path $g.key)) { continue }
    $ap = Approved $g.root $g.sub; $k = Get-Item $g.key
    foreach ($n in $k.GetValueNames()) {
      if ($n -eq '') { continue }
      $items += [ordered]@{ location = $g.loc; name = $n; command = Short $k.GetValue($n) 200; enabled = if ($ap.ContainsKey($n)) { $ap[$n] } else { $true } }
    }
  }
  $folders = @(
    @{ loc = 'Startup_User'; dir = "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup"; root = 'HKCU:' },
    @{ loc = 'Startup_Common'; dir = "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\StartUp"; root = 'HKLM:' })
  foreach ($g in $folders) {
    if (-not (Test-Path $g.dir)) { continue }
    $ap = Approved $g.root 'StartupFolder'
    foreach ($f in @(Get-ChildItem $g.dir -File -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne 'desktop.ini' })) {
      $items += [ordered]@{ location = $g.loc; name = $f.Name; command = $f.FullName; enabled = if ($ap.ContainsKey($f.Name)) { $ap[$f.Name] } else { $true } }
    }
  }
  $items
}

Section 'scheduledTasks' {
  @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskPath -notlike '\Microsoft\*' -and "$($_.State)" -ne 'Disabled' } | ForEach-Object {
    [ordered]@{ path = $_.TaskPath; name = $_.TaskName; author = $_.Author; state = "$($_.State)"
      runs = Short (($_.Actions | ForEach-Object { "$($_.Execute) $($_.Arguments)".Trim() }) -join ' | ') 200
      triggers = (($_.Triggers | ForEach-Object { $_.CimClass.CimClassName -replace 'MSFT_Task', '' -replace 'Trigger', '' }) -join ',') }
  })
}

Section 'services' {
  $optional = 'DiagTrack','dmwappushservice','SysMain','WSearch','Fax','XblAuthManager','XblGameSave','XboxNetApiSvc','XboxGipSvc','MapsBroker','RetailDemo','WMPNetworkSvc','RemoteRegistry','lfsvc','PhoneSvc','WerSvc','Spooler','TabletInputService','wisvc','PcaSvc','WbioSrvc','icssvc','SEMgrSvc','WpcMonSvc','TrkWks','diagnosticshub.standardcollector.service','SharedAccess','WalletService'
  @(Get-CimInstance Win32_Service | Where-Object { ($_.StartMode -eq 'Auto' -and $_.PathName -notmatch '\\Windows\\') -or ($optional -contains $_.Name) } | ForEach-Object {
    [ordered]@{ name = $_.Name; displayName = $_.DisplayName; state = $_.State; startMode = $_.StartMode; thirdParty = ($_.PathName -notmatch '\\Windows\\'); path = Short $_.PathName 160 }
  })
}

Section 'apps' {
  $hints = 'Clipchamp','BingNews','BingWeather','BingFinance','BingSports','BingSearch','GamingApp','XboxApp','Xbox.TCUI','XboxGamingOverlay','XboxGameOverlay','XboxSpeechToTextOverlay','GetHelp','Getstarted','MicrosoftOfficeHub','MicrosoftSolitaireCollection','People','PowerAutomateDesktop','Todos','WindowsFeedbackHub','WindowsMaps','ZuneMusic','ZuneVideo','YourPhone','MixedReality','SkypeApp','3DBuilder','Microsoft3DViewer','OneConnect','Messaging','Wallet','Disney','Spotify','TikTok','Instagram','Facebook','Twitter','CandyCrush','king.com','Netflix','LinkedIn','AmazonPrime','McAfee','Norton','ExpressVPN','Bytedance','Copilot','DevHome','OutlookForWindows','MSTeams','Family','549981C3F5F10'
  $prov = @(Get-AppxProvisionedPackage -Online -ErrorAction SilentlyContinue | ForEach-Object { $_.DisplayName })
  @(Get-AppxPackage -AllUsers -ErrorAction SilentlyContinue | Where-Object { -not $_.IsFramework -and -not $_.NonRemovable -and "$($_.SignatureKind)" -ne 'System' } |
    Group-Object Name | ForEach-Object {
      $p = $_.Group[0]; $n = $p.Name
      [ordered]@{ name = $n; version = "$($p.Version)"; publisher = ($p.Publisher -replace '^CN=([^,]+).*', '$1'); provisioned = ($prov -contains $n)
        likelyBloat = [bool]($hints | Where-Object { $n -like "*$_*" }) }
    })
}

Section 'programs' {
  $keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*','HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
  @(Get-ItemProperty $keys -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -and $_.SystemComponent -ne 1 -and -not $_.ParentKeyName } |
    Sort-Object DisplayName -Unique | ForEach-Object {
      [ordered]@{ name = $_.DisplayName; publisher = $_.Publisher; version = $_.DisplayVersion; installed = $_.InstallDate; sizeMB = if ($_.EstimatedSize) { [math]::Round($_.EstimatedSize / 1024) } else { $null } }
    })
}

Section 'power' {
  $pw = Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Power' -ErrorAction SilentlyContinue
  [ordered]@{ activePlan = ((powercfg /getactivescheme) -join ' ').Trim(); plans = ((powercfg /list) | Where-Object { $_ -match 'GUID' }) -join ' ; '
    fastStartup = $pw.HiberbootEnabled; hibernationEnabled = (Test-Path "$env:SystemDrive\hiberfil.sys") }
}

Section 'battery' {
  if (-not (Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue)) { return $null }
  $s = @(Get-CimInstance -Namespace root/wmi -ClassName BatteryStaticData -ErrorAction SilentlyContinue)[0]
  $f = @(Get-CimInstance -Namespace root/wmi -ClassName BatteryFullChargedCapacity -ErrorAction SilentlyContinue)[0]
  $c = @(Get-CimInstance -Namespace root/wmi -ClassName BatteryCycleCount -ErrorAction SilentlyContinue)[0]
  [ordered]@{ designmWh = $s.DesignedCapacity; fullChargemWh = $f.FullChargedCapacity; cycles = $c.CycleCount
    healthPct = if ($s.DesignedCapacity) { [math]::Round(100 * $f.FullChargedCapacity / $s.DesignedCapacity) } else { $null } }
}

Section 'security' {
  $mp = Get-MpComputerStatus -ErrorAction SilentlyContinue
  $av = @(Get-CimInstance -Namespace root/SecurityCenter2 -ClassName AntiVirusProduct -ErrorAction SilentlyContinue | ForEach-Object { $_.displayName })
  $th = @(Get-MpThreatDetection -ErrorAction SilentlyContinue | Sort-Object InitialDetectionTime -Descending | Select-Object -First 10 | ForEach-Object {
    [ordered]@{ when = $_.InitialDetectionTime.ToString('s'); threatId = $_.ThreatID; resources = Short ($_.Resources -join ' ; ') 200 } })
  $names = @{}; foreach ($t in @(Get-MpThreat -ErrorAction SilentlyContinue)) { $names["$($t.ThreatID)"] = $t.ThreatName }
  foreach ($t in $th) { $t['name'] = $names["$($t.threatId)"] }
  [ordered]@{ antivirusProducts = $av; defenderEnabled = $mp.AntivirusEnabled; realTime = $mp.RealTimeProtectionEnabled; tamperProtected = $mp.IsTamperProtected
    signaturesUpdated = if ($mp.AntivirusSignatureLastUpdated) { $mp.AntivirusSignatureLastUpdated.ToString('s') } else { $null }
    quickScanAgeDays = $mp.QuickScanAge; fullScanAgeDays = $mp.FullScanAge; recentThreats = $th
    uacEnabled = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -ErrorAction SilentlyContinue).EnableLUA
    firewall = @(Get-NetFirewallProfile -ErrorAction SilentlyContinue | ForEach-Object { "$($_.Name)=$($_.Enabled)" }) -join ', ' }
}

Section 'updates' {
  $cbs = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending'
  $wu = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired'
  [ordered]@{ rebootPending = ($cbs -or $wu)
    recentHotfixes = @(Get-HotFix -ErrorAction SilentlyContinue | Where-Object { $_.InstalledOn } | Sort-Object InstalledOn -Descending | Select-Object -First 5 | ForEach-Object { "$($_.HotFixID) $($_.InstalledOn.ToString('yyyy-MM-dd'))" }) }
}

Section 'restorePoints' {
  $rp = @(Get-ComputerRestorePoint -ErrorAction SilentlyContinue)
  [ordered]@{ count = $rp.Count; latest = if ($rp.Count) { "$($rp[-1].Description) @ $($rp[-1].CreationTime)" } else { $null } }
}

Section 'events' {
  $since7 = (Get-Date).AddDays(-7); $since30 = (Get-Date).AddDays(-30)
  $ev = @(Get-WinEvent -FilterHashtable @{ LogName = 'System', 'Application'; Level = 1, 2; StartTime = $since7 } -MaxEvents 3000 -ErrorAction SilentlyContinue)
  $top = @($ev | Group-Object ProviderName | Sort-Object Count -Descending | Select-Object -First 15 | ForEach-Object {
    $e = $_.Group[0]; [ordered]@{ source = $_.Name; count = $_.Count; id = $e.Id; log = $e.LogName; sample = Short $e.Message 220 } })
  $crashes = @(Get-WinEvent -FilterHashtable @{ LogName = 'Application'; Id = 1000; StartTime = $since30 } -MaxEvents 500 -ErrorAction SilentlyContinue |
    ForEach-Object { $_.Properties[0].Value } | Group-Object | Sort-Object Count -Descending | Select-Object -First 8 | ForEach-Object { "$($_.Name) x$($_.Count)" })
  function CountEv($h) { @(Get-WinEvent -FilterHashtable $h -MaxEvents 200 -ErrorAction SilentlyContinue).Count }
  [ordered]@{ errorsLast7Days = $ev.Count; topSources = $top; appCrashes30d = $crashes
    unexpectedShutdowns30d = CountEv @{ LogName = 'System'; ProviderName = 'Microsoft-Windows-Kernel-Power'; Id = 41; StartTime = $since30 }
    bluescreens30d = CountEv @{ LogName = 'System'; ProviderName = 'Microsoft-Windows-WER-SystemErrorReporting'; Id = 1001; StartTime = $since30 }
    diskErrors30d = CountEv @{ LogName = 'System'; ProviderName = 'disk', 'Ntfs', 'stornvme', 'storahci'; Level = 1, 2, 3; StartTime = $since30 } }
}

Section 'problemDevices' {
  @(Get-CimInstance Win32_PnPEntity -ErrorAction SilentlyContinue | Where-Object { $_.ConfigManagerErrorCode -ne 0 } | ForEach-Object {
    [ordered]@{ name = $_.Name; class = $_.PNPClass; errorCode = $_.ConfigManagerErrorCode } })
}

Section 'topProcesses' {
  @(Get-Process | Sort-Object WorkingSet64 -Descending | Select-Object -First 12 | ForEach-Object {
    [ordered]@{ name = $_.ProcessName; memMB = [math]::Round($_.WorkingSet64 / 1MB); cpuSec = [math]::Round($_.CPU, 1) } })
}

Section 'privacy' {
  function V($p, $n) { (Get-ItemProperty $p -ErrorAction SilentlyContinue).$n }
  $cdm = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\ContentDeliveryManager'
  [ordered]@{
    advertisingId = V 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\AdvertisingInfo' 'Enabled'
    startSuggestions = V $cdm 'SystemPaneSuggestionsEnabled'; silentAppInstalls = V $cdm 'SilentInstalledAppsEnabled'
    tailoredExperiences = V 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Privacy' 'TailoredExperiencesWithDiagnosticDataEnabled'
    telemetryPolicy = V 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\DataCollection' 'AllowTelemetry'
    bingInStart = V 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Search' 'BingSearchEnabled'
    lockscreenTips = V $cdm 'RotatingLockScreenOverlayEnabled'
  }
}

Out-Result $r
