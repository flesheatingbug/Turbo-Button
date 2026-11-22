Update-MpSignature -ErrorAction Stop
$mp = Get-MpComputerStatus
Out-Result @{ signatureVersion = $mp.AntivirusSignatureVersion; updated = $mp.AntivirusSignatureLastUpdated.ToString('s') }
