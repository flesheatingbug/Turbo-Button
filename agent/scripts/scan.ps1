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
    partOfDomain = [bool]$cs.PartOfDomain; domain = if ($cs.PartOfDomain) { $cs.Domain } else { $null }
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
      $how = if ($_.QuietUninstallString) { 'quiet' } elseif ("$($_.UninstallString)" -match 'msiexec') { 'msi' } elseif ($_.UninstallString) { 'interactive' } else { 'none' }
      [ordered]@{ name = $_.DisplayName; publisher = $_.Publisher; version = $_.DisplayVersion; installed = $_.InstallDate; sizeMB = if ($_.EstimatedSize) { [math]::Round($_.EstimatedSize / 1024) } else { $null }; uninstall = $how }
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
    quickScanAgeDays = if ($mp.QuickScanAge -ge 100000) { 'never' } else { $mp.QuickScanAge }
    fullScanAgeDays = if ($mp.FullScanAge -ge 100000) { 'never' } else { $mp.FullScanAge }; recentThreats = $th
    uacEnabled = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -ErrorAction SilentlyContinue).EnableLUA
    firewall = @(Get-NetFirewallProfile -ErrorAction SilentlyContinue | ForEach-Object { "$($_.Name)=$($_.Enabled)" }) -join ', ' }
}

Section 'updates' {
  $cbs = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending'
  $wu = Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired'
  $last = $null; try { $last = (New-Object -ComObject Microsoft.Update.AutoUpdate).Results.LastInstallationSuccessDate } catch {}
  [ordered]@{ rebootPending = ($cbs -or $wu)
    lastUpdateInstalled = if ($last -and $last.Year -gt 2000) { $last.ToString('s') } else { $null }
    daysSinceLastUpdate = if ($last -and $last.Year -gt 2000) { [math]::Round(((Get-Date) - $last).TotalDays) } else { $null }
    wingetAvailable = [bool](Get-Winget)
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

function EvData($e) { $d = @{}; foreach ($n in ([xml]$e.ToXml()).Event.EventData.Data) { $d[$n.Name] = $n.'#text' }; $d }

Section 'bootPerformance' {
  $log = 'Microsoft-Windows-Diagnostics-Performance/Operational'
  $boots = @(Get-WinEvent -FilterHashtable @{ LogName = $log; Id = 100 } -MaxEvents 5 -ErrorAction SilentlyContinue | ForEach-Object {
    $d = EvData $_
    [ordered]@{ when = $_.TimeCreated.ToString('s'); bootSec = [math]::Round([double]$d['BootTime'] / 1000, 1)
      toDesktopSec = [math]::Round([double]$d['MainPathBootTime'] / 1000, 1); afterLoginSec = [math]::Round([double]$d['BootPostBootTime'] / 1000, 1) } })
  $kinds = @{ 101 = 'app'; 102 = 'driver'; 103 = 'service'; 106 = 'background task'; 109 = 'device' }
  $slow = @(Get-WinEvent -FilterHashtable @{ LogName = $log; Id = 101, 102, 103, 106, 109 } -MaxEvents 300 -ErrorAction SilentlyContinue | ForEach-Object {
    $d = EvData $_
    $name = @($d['FriendlyName'], $d['Name'], $d['FileName']) | Where-Object { $_ } | Select-Object -First 1
    [pscustomobject]@{ kind = $kinds[$_.Id]; name = "$name"; delayMs = [double]$d['DegradationTime'] } } |
    Where-Object { $_.name } | Group-Object kind, name | ForEach-Object {
      [ordered]@{ kind = $_.Group[0].kind; name = $_.Group[0].name; worstDelaySec = [math]::Round(($_.Group | Measure-Object delayMs -Maximum).Maximum / 1000, 1); times = $_.Count } } |
    Sort-Object { $_.worstDelaySec } -Descending | Select-Object -First 10)
  [ordered]@{ recentBoots = $boots; slowdowns = $slow }
}

Section 'crashes' {
  $since = (Get-Date).AddDays(-90)
  $bc = @(Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Microsoft-Windows-WER-SystemErrorReporting'; Id = 1001; StartTime = $since } -MaxEvents 30 -ErrorAction SilentlyContinue | ForEach-Object {
    $txt = "$($_.Properties[0].Value) $($_.Message)"
    $m = [regex]::Match($txt, '0x([0-9a-fA-F]{8,16})')
    $code = if ($m.Success) { '0x' + ($m.Groups[1].Value.TrimStart('0')).ToUpper() } else { $null }
    if ($code -eq '0x') { $code = '0x0' }
    [ordered]@{ when = $_.TimeCreated.ToString('s'); code = $code; dump = ([regex]::Match($txt, '[A-Za-z]:\\[^\s"]+\.dmp')).Value } })
  $dumps = @(Get-ChildItem "$env:windir\Minidump" -Filter *.dmp -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 10 | ForEach-Object { "$($_.Name) $($_.LastWriteTime.ToString('s'))" })
  $whea = @(Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Microsoft-Windows-WHEA-Logger'; StartTime = $since } -MaxEvents 50 -ErrorAction SilentlyContinue).Count
  [ordered]@{ bugchecks90d = $bc; minidumps = $dumps; hardwareErrors90d = $whea }
}

Section 'performance' {
  $cores = [Environment]::ProcessorCount
  $t1 = @{}; foreach ($p in Get-Process) { try { $t1[$p.Id] = $p.TotalProcessorTime.TotalMilliseconds } catch {} }
  $sw = [Diagnostics.Stopwatch]::StartNew(); $samples = @()
  for ($i = 0; $i -lt 3; $i++) { $samples += [double](Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor -Filter "Name='_Total'").PercentProcessorTime; Start-Sleep -Milliseconds 900 }
  $el = $sw.Elapsed.TotalMilliseconds
  $top = @(Get-Process | ForEach-Object { $a = $t1[$_.Id]; if ($null -ne $a) { try { [pscustomobject]@{ name = $_.ProcessName; pct = [math]::Round(100 * ($_.TotalProcessorTime.TotalMilliseconds - $a) / ($el * $cores), 1) } } catch {} } } |
    Where-Object { $_.pct -ge 1 } | Sort-Object pct -Descending | Select-Object -First 6 | ForEach-Object { "$($_.name) $($_.pct)%" })
  $m = Get-CimInstance Win32_PerfFormattedData_PerfOS_Memory
  $temps = @(Get-CimInstance -Namespace root/wmi -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue | ForEach-Object { [math]::Round($_.CurrentTemperature / 10 - 273.15) } | Where-Object { $_ -gt 0 -and $_ -lt 120 })
  [ordered]@{ cpuNowPct = [math]::Round(($samples | Measure-Object -Average).Average); topCpu = $top
    availableMB = $m.AvailableMBytes; committedPct = $m.PercentCommittedBytesInUse; pagesPerSec = $m.PagesPerSec; thermalZonesC = $temps }
}

Section 'memoryTest' {
  $e = Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Microsoft-Windows-MemoryDiagnostics-Results' } -MaxEvents 1 -ErrorAction SilentlyContinue
  if ($e) { [ordered]@{ when = $e.TimeCreated.ToString('s'); id = $e.Id; message = Short $e.Message 200 } } else { $null }
}

Section 'browsersAndNetwork' {
  $roots = [ordered]@{ chrome = 'Google\Chrome\User Data'; edge = 'Microsoft\Edge\User Data'; brave = 'BraveSoftware\Brave-Browser\User Data' }
  $exts = @(); $settings = @()
  foreach ($u in @(Get-ChildItem 'C:\Users' -Directory -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -notin 'Public', 'Default', 'Default User', 'All Users' })) {
    foreach ($b in $roots.Keys) {
      $root = Join-Path $u.FullName ('AppData\Local\' + $roots[$b])
      if (-not (Test-Path $root)) { continue }
      foreach ($prof in @(Get-ChildItem $root -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq 'Default' -or $_.Name -like 'Profile *' })) {
        foreach ($e in @(Get-ChildItem (Join-Path $prof.FullName 'Extensions') -Directory -ErrorAction SilentlyContinue)) {
          $ver = Get-ChildItem $e.FullName -Directory -ErrorAction SilentlyContinue | Sort-Object Name -Descending | Select-Object -First 1
          if (-not $ver) { continue }
          $name = $null
          try {
            $j = Get-Content (Join-Path $ver.FullName 'manifest.json') -Raw -ErrorAction Stop | ConvertFrom-Json
            $name = $j.name
            if ("$name" -like '__MSG_*') {
              $key = "$name" -replace '^__MSG_(.+)__$', '$1'
              foreach ($loc in @($j.default_locale, 'en', 'en_US') | Where-Object { $_ }) {
                $mp = Join-Path $ver.FullName "_locales\$loc\messages.json"
                if (Test-Path $mp) { $msgs = Get-Content $mp -Raw | ConvertFrom-Json; $pr = $msgs.PSObject.Properties | Where-Object { $_.Name -ieq $key } | Select-Object -First 1; if ($pr) { $name = $pr.Value.message; break } }
              }
            }
          } catch {}
          if ($name) { $exts += [ordered]@{ browser = $b; user = $u.Name; profile = $prof.Name; id = $e.Name; name = Short $name 80 } }
        }
        $pf = Join-Path $prof.FullName 'Preferences'
        if (Test-Path $pf) {
          $raw = Get-Content $pf -Raw -ErrorAction SilentlyContinue
          $search = ([regex]::Match("$raw", '"default_search_provider_data"\s*:\s*\{\s*"template_url_data"\s*:\s*\{[^}]*?"url"\s*:\s*"([^"]+)"')).Groups[1].Value
          $hpage = ([regex]::Match("$raw", '"homepage"\s*:\s*"([^"]*)"')).Groups[1].Value
          $start = ([regex]::Match("$raw", '"startup_urls"\s*:\s*\[([^\]]*)\]')).Groups[1].Value
          if ($search -or $hpage -or $start) { $settings += [ordered]@{ browser = $b; user = $u.Name; profile = $prof.Name; searchUrl = $search; homepage = $hpage; startupUrls = Short $start 200 } }
        }
      }
    }
    $ffp = Join-Path $u.FullName 'AppData\Roaming\Mozilla\Firefox\Profiles'
    foreach ($prof in @(Get-ChildItem $ffp -Directory -ErrorAction SilentlyContinue)) {
      try {
        $j = Get-Content (Join-Path $prof.FullName 'extensions.json') -Raw -ErrorAction Stop | ConvertFrom-Json
        foreach ($a in @($j.addons | Where-Object { $_.location -eq 'app-profile' -and $_.type -eq 'extension' })) {
          $exts += [ordered]@{ browser = 'firefox'; user = $u.Name; profile = $prof.Name; id = $a.id; name = Short $a.defaultLocale.name 80; active = $a.active }
        }
      } catch {}
    }
  }
  $pol = @()
  foreach ($k in 'HKLM:\SOFTWARE\Policies\Google\Chrome', 'HKCU:\SOFTWARE\Policies\Google\Chrome', 'HKLM:\SOFTWARE\Policies\Microsoft\Edge', 'HKCU:\SOFTWARE\Policies\Microsoft\Edge', 'HKLM:\SOFTWARE\Policies\BraveSoftware\Brave', 'HKCU:\SOFTWARE\Policies\BraveSoftware\Brave') {
    if (-not (Test-Path $k)) { continue }
    $vals = @((Get-Item $k).GetValueNames()) ; $subs = @(Get-ChildItem $k -ErrorAction SilentlyContinue | ForEach-Object { $_.PSChildName })
    $forced = @(); $fk = Join-Path $k 'ExtensionInstallForcelist'
    if (Test-Path $fk) { $it = Get-Item $fk; $forced = @($it.GetValueNames() | ForEach-Object { Short $it.GetValue($_) 120 }) }
    $p = Get-ItemProperty $k -ErrorAction SilentlyContinue
    $pol += [ordered]@{ key = $k; values = $vals; subkeys = $subs; forcedExtensions = $forced; homepage = $p.HomepageLocation; searchUrl = $p.DefaultSearchProviderSearchURL }
  }
  $is = Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
  $winhttp = ((& netsh.exe winhttp show proxy 2>$null) | Where-Object { "$_".Trim() }) -join ' '
  $dns = @()
  foreach ($a in @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object Status -eq 'Up')) {
    $srv = (Get-DnsClientServerAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue).ServerAddresses
    $static = (Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\$($a.InterfaceGuid)" -ErrorAction SilentlyContinue).NameServer
    $dns += [ordered]@{ adapter = $a.Name; servers = @($srv); manual = [bool]$static }
  }
  $hosts = @(Get-Content "$env:windir\System32\drivers\etc\hosts" -ErrorAction SilentlyContinue | Where-Object { $_.Trim() -and -not $_.Trim().StartsWith('#') } | ForEach-Object { ($_ -replace '\s+', ' ').Trim() })
  [ordered]@{ extensions = $exts; browserSettings = $settings; browserPolicies = $pol
    proxy = [ordered]@{ enabled = $is.ProxyEnable; server = $is.ProxyServer; autoConfigUrl = $is.AutoConfigURL; winhttp = Short $winhttp 160 }
    dns = $dns; hostsEntries = @($hosts | Select-Object -First 40); hostsEntryCount = $hosts.Count }
}

Out-Result $r
