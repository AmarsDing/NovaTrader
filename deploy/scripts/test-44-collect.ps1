# Post-breaker-reset collect sweep
$ErrorActionPreference = 'Continue'
$Base = 'http://192.168.4.44:22001'
$login = Invoke-RestMethod -Uri "$Base/api/v1/login" -Method POST -ContentType 'application/json' -Body '{"user_name":"admin","password":"123456"}'
$h = @{ Authorization = "Bearer $($login.token)" }

Write-Host '=== health ==='
(Invoke-RestMethod "$Base/healthz") | ConvertTo-Json -Compress

$domains = @(
  'security',
  'limit_pool',
  'snapshot',
  'daily_bar',
  'sector',
  'hot_rank',
  'macro',
  'overseas',
  'flash',
  'news',
  'finance'
)

foreach ($d in $domains) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  try {
    $r = Invoke-WebRequest -Uri "$Base/api/datahub/collect/$d" -Method POST -Headers $h -ContentType 'application/json' -Body '{}' -TimeoutSec 300 -UseBasicParsing
    $sw.Stop()
    Write-Host ("[PASS] {0} {1}ms {2}" -f $d, $sw.ElapsedMilliseconds, $r.Content.Substring(0, [Math]::Min(220, $r.Content.Length)))
  } catch {
    $sw.Stop()
    $code = 0; $body = $_.Exception.Message
    if ($_.Exception.Response) { try { $code = [int]$_.Exception.Response.StatusCode } catch {} }
    if ($_.ErrorDetails.Message) { $body = $_.ErrorDetails.Message }
    Write-Host ("[WARN] {0} {1}ms code={2} {3}" -f $d, $sw.ElapsedMilliseconds, $code, $body.Substring(0, [Math]::Min(260, $body.Length)))
  }
}

Write-Host ''
Write-Host '=== sources summary ==='
$s = Invoke-RestMethod -Uri "$Base/api/datahub/sources" -Headers $h
$s.sources | Group-Object source | ForEach-Object {
  $states = $_.Group | Group-Object state | ForEach-Object { "$($_.Name)=$($_.Count)" }
  Write-Host ("{0}: enabled={1} {2}" -f $_.Name, $_.Group[0].enabled, ($states -join ' '))
}
