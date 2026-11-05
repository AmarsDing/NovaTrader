# Data-source focused probe after akshare enable
$ErrorActionPreference = 'Continue'
$Base = 'http://192.168.4.44:22001'
$login = Invoke-RestMethod -Uri "$Base/api/v1/login" -Method POST -ContentType 'application/json' -Body '{"user_name":"admin","password":"123456"}'
$h = @{ Authorization = "Bearer $($login.token)" }

Write-Host '=== datahub sources (akshare/baostock/eastmoney/tushare) ==='
$s = Invoke-RestMethod -Uri "$Base/api/datahub/sources" -Headers $h
$s.sources |
  Where-Object { $_.source -match '^(akshare|baostock|eastmoney|tushare|sina|tencent)$' } |
  Group-Object source |
  ForEach-Object {
    $open = @($_.Group | Where-Object { $_.state -eq 'open' }).Count
    $closed = @($_.Group | Where-Object { $_.state -eq 'closed' }).Count
    $disabled = @($_.Group | Where-Object { $_.state -eq 'disabled' }).Count
    $unavail = @($_.Group | Where-Object { $_.state -eq 'unavailable' }).Count
    $domains = ($_.Group | ForEach-Object { $_.domain }) -join ','
    Write-Host ("{0}: enabled={1} open={2} closed={3} disabled={4} unavailable={5} domains={6}" -f $_.Name, ($_.Group[0].enabled), $open, $closed, $disabled, $unavail, $domains)
  }

Write-Host ''
Write-Host '=== collect attempts ==='
foreach ($domain in @('security', 'daily_bar', 'snapshot', 'limit_pool', 'macro', 'overseas')) {
  try {
    $r = Invoke-WebRequest -Uri "$Base/api/datahub/collect/$domain" -Method POST -Headers $h -ContentType 'application/json' -Body '{}' -TimeoutSec 180 -UseBasicParsing
    Write-Host ("[PASS] collect/{0} -> {1} {2}" -f $domain, $r.StatusCode, $r.Content.Substring(0, [Math]::Min(180, $r.Content.Length)))
  } catch {
    $code = 0
    $body = $_.Exception.Message
    if ($_.Exception.Response) { try { $code = [int]$_.Exception.Response.StatusCode } catch {} }
    if ($_.ErrorDetails.Message) { $body = $_.ErrorDetails.Message }
    Write-Host ("[WARN] collect/{0} -> {1} {2}" -f $domain, $code, $body.Substring(0, [Math]::Min(220, $body.Length)))
  }
}

Write-Host ''
Write-Host '=== business reads ==='
@(
  '/api/market/watchlist',
  '/api/market/emotion',
  '/api/risk/state',
  '/api/trade/account',
  '/api/brain/stats',
  '/api/intel/hotwords?limit=5',
  '/api/intel/stocks/600519/timeline?limit=5',
  '/api/strategy/signals?limit=5',
  '/api/notify/messages?limit=5',
  '/api/ops/status'
) | ForEach-Object {
  try {
    $r = Invoke-WebRequest -Uri "$Base$_" -Headers $h -TimeoutSec 30 -UseBasicParsing
    Write-Host ("[PASS] {0} -> {1} len={2}" -f $_, $r.StatusCode, $r.Content.Length)
  } catch {
    $code = 0
    if ($_.Exception.Response) { try { $code = [int]$_.Exception.Response.StatusCode } catch {} }
    Write-Host ("[WARN] {0} -> {1}" -f $_, $code)
  }
}
