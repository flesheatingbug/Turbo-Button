$arg = if ($env:PCM_enabled -eq '1') { 'on' } else { 'off' }
$before = (Get-Item "$env:SystemDrive\hiberfil.sys" -Force -ErrorAction SilentlyContinue).Length
powercfg /hibernate $arg
if ($LASTEXITCODE -ne 0) { throw "powercfg /hibernate $arg failed ($LASTEXITCODE)" }
Out-Result @{ hibernation = $arg; hiberfilBytesBefore = [int64]$before }
