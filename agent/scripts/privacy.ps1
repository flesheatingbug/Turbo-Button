function SetD($path, $name, $val) {
  if (-not (Test-Path $path)) { New-Item -Path $path -Force | Out-Null }
  $prev = (Get-ItemProperty $path -ErrorAction SilentlyContinue).$name
  New-ItemProperty -Path $path -Name $name -Value $val -PropertyType DWord -Force | Out-Null
  "$path\$name : $prev -> $val"
}
$cdm = 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\ContentDeliveryManager'
$changes = switch ($env:PCM_tweak) {
  'advertising_id' { SetD 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\AdvertisingInfo' 'Enabled' 0
                     SetD 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\AdvertisingInfo' 'DisabledByGroupPolicy' 1 }
  'start_suggestions' { foreach ($n in 'SystemPaneSuggestionsEnabled','SilentInstalledAppsEnabled','SoftLandingEnabled','SubscribedContent-338388Enabled','SubscribedContent-338389Enabled','SubscribedContent-338393Enabled','SubscribedContent-353694Enabled','SubscribedContent-353696Enabled','PreInstalledAppsEnabled','OemPreInstalledAppsEnabled') { SetD $cdm $n 0 }
                        SetD 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\Advanced' 'Start_IrisRecommendations' 0
                        SetD 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\CloudContent' 'DisableWindowsConsumerFeatures' 1 }
  'tailored_experiences' { SetD 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Privacy' 'TailoredExperiencesWithDiagnosticDataEnabled' 0 }
  'telemetry_minimum' { SetD 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\DataCollection' 'AllowTelemetry' 1 }
  'bing_search' { SetD 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Search' 'BingSearchEnabled' 0
                  SetD 'HKCU:\SOFTWARE\Policies\Microsoft\Windows\Explorer' 'DisableSearchBoxSuggestions' 1 }
  'lockscreen_tips' { SetD $cdm 'RotatingLockScreenOverlayEnabled' 0; SetD $cdm 'SubscribedContent-338387Enabled' 0 }
  default { throw 'Unknown tweak' }
}
Out-Result @{ tweak = $env:PCM_tweak; changes = @($changes); note = 'Some changes apply after sign-out or restart.' }
