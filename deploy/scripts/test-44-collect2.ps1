$ErrorActionPreference = 'Continue'
$Base = 'http://192.168.4.44:22001'
$login = Invoke-RestMethod -Uri "$Base/api/v1/login" -Method POST -ContentType 'application/json' -Body '{"user_name":"admin","password":"123456"}'
$h = @{ Authorization = "Bearer $($login.token)" }

function Collect([string]$domain, $body) {
  $json = if ($null -eq $body) { '{}' } else { $body | ConvertTo-Json -Compress }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  try {
    $r = Invoke-WebRequest -Uri "$Base/api/datahub/collect/$domain" -Method POST -Headers $h -ContentType 'application/json' -Body $json -TimeoutSec 300 -UseBasicParsing
    $sw.Stop()
    Write-Host ("[PASS] {0} {1}ms {2}" -f $domain, $sw.ElapsedMilliseconds, $r.Content.Substring(0, [Math]::Min(240, $r.Content.Length)))
  } catch {
    $sw.Stop()
    $code = 0; $bodyText = $_.Exception.Message
    if ($_.Exception.Response) { try { $code = [int]$_.Exception.Response.StatusCode } catch {} }
    if ($_.ErrorDetails.Message) { $bodyText = $_.ErrorDetails.Message }
    Write-Host ("[WARN] {0} {1}ms code={2} {3}" -f $domain, $sw.ElapsedMilliseconds, $code, $bodyText.Substring(0, [Math]::Min(280, $bodyText.Length)))
  }
}

Write-Host '=== collect after eastmoney timeout=60s ==='
Collect 'security' $null
Collect 'snapshot' $null
Collect 'daily_bar' @{ date = '2026-10-28' }
Collect 'limit_pool' @{ date = '2026-10-28' }
Collect 'macro' $null
Collect 'overseas' $null
Collect 'hot_rank' $null
Collect 'sector' $null

Write-Host ''
Write-Host '=== source states ==='
$s = Invoke-RestMethod -Uri "$Base/api/datahub/sources" -Headers $h
$s.sources | Group-Object source | ForEach-Object {
  $states = ($_.Group | Group-Object state | ForEach-Object { "$($_.Name)=$($_.Count)" }) -join ' '
  Write-Host ("{0}: {1}" -f $_.Name, $states)
}
