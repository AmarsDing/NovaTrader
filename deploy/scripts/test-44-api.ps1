# NovaTrader 44 API full probe
$ErrorActionPreference = 'Continue'
$Base = if ($env:NT_BASE) { $env:NT_BASE } else { 'http://192.168.4.44:22001' }
$Web = if ($env:NT_WEB) { $env:NT_WEB } else { 'http://192.168.4.44:9302' }

$script:pass = 0
$script:fail = 0
$script:warn = 0
$results = New-Object System.Collections.Generic.List[string]

function Record([string]$level, [string]$name, [string]$detail) {
  $line = "[$level] $name -- $detail"
  [void]$results.Add($line)
  Write-Host $line
  if ($level -eq 'PASS') { $script:pass++ }
  elseif ($level -eq 'FAIL') { $script:fail++ }
  else { $script:warn++ }
}

function TryGet([string]$url, [hashtable]$headers, [int]$timeoutSec = 15) {
  try {
    $r = Invoke-WebRequest -Uri $url -Headers $headers -Method GET -TimeoutSec $timeoutSec -UseBasicParsing
    return @{ ok = $true; code = [int]$r.StatusCode; body = [string]$r.Content }
  } catch {
    $code = 0
    if ($_.Exception.Response) { try { $code = [int]$_.Exception.Response.StatusCode } catch {} }
    $body = [string]$_.ErrorDetails.Message
    if (-not $body) { $body = [string]$_.Exception.Message }
    return @{ ok = $false; code = $code; body = $body }
  }
}

function TryJson([string]$method, [string]$url, [hashtable]$headers, $bodyObj, [int]$timeoutSec = 30) {
  try {
    $params = @{
      Uri             = $url
      Method          = $method
      Headers         = $headers
      TimeoutSec      = $timeoutSec
      UseBasicParsing = $true
    }
    if ($null -ne $bodyObj) {
      $params.ContentType = 'application/json; charset=utf-8'
      $params.Body = ($bodyObj | ConvertTo-Json -Compress -Depth 8)
    }
    $r = Invoke-WebRequest @params
    $parsed = $null
    try { $parsed = $r.Content | ConvertFrom-Json } catch {}
    return @{ ok = $true; code = [int]$r.StatusCode; body = [string]$r.Content; json = $parsed }
  } catch {
    $code = 0
    $body = [string]$_.ErrorDetails.Message
    if ($_.Exception.Response) {
      try { $code = [int]$_.Exception.Response.StatusCode } catch {}
      try {
        $stream = $_.Exception.Response.GetResponseStream()
        $reader = New-Object System.IO.StreamReader($stream)
        $body = $reader.ReadToEnd()
      } catch {}
    }
    if (-not $body) { $body = [string]$_.Exception.Message }
    $parsed = $null
    try { $parsed = $body | ConvertFrom-Json } catch {}
    return @{ ok = $false; code = $code; body = $body; json = $parsed }
  }
}

function Clip([string]$s, [int]$n = 160) {
  if (-not $s) { return '' }
  if ($s.Length -le $n) { return $s }
  return $s.Substring(0, $n)
}

Write-Host "=== NovaTrader 44 API Test ==="
Write-Host "Base=$Base Web=$Web"
Write-Host ""

# 0 web
$w = TryGet $Web @{} 10
if ($w.ok -and ($w.body -match 'html|NovaTrader|script')) { Record 'PASS' 'web.spa' "HTTP $($w.code)" }
else { Record 'FAIL' 'web.spa' "code=$($w.code) $(Clip $w.body 120)" }

# 1 healthz
$h = TryGet "$Base/healthz" @{} 8
if ($h.ok -and ($h.body -match 'ok')) { Record 'PASS' 'healthz' (Clip $h.body 80) }
else { Record 'FAIL' 'healthz' "code=$($h.code) $(Clip $h.body 120)" }

# 2 login
$login = TryJson 'POST' "$Base/api/v1/login" @{} @{ user_name = 'admin'; password = '123456' } 15
$token = $null
if ($login.ok -and $login.json -and $login.json.token) {
  $token = [string]$login.json.token
  $role = ''
  if ($login.json.user) { $role = [string]$login.json.user.role }
  Record 'PASS' 'login.admin' "role=$role"
} else {
  Record 'FAIL' 'login.admin' "code=$($login.code) $(Clip $login.body 200)"
  Write-Host "PASS=$($script:pass) FAIL=$($script:fail) WARN=$($script:warn)"
  exit 1
}
$Auth = @{ Authorization = "Bearer $token" }

# 3 ops
$ops = TryJson 'GET' "$Base/api/ops/status" $Auth $null 20
if ($ops.ok) {
  $srcList = @()
  if ($ops.json.sources) { $srcList = @($ops.json.sources) }
  Record 'PASS' 'ops.status' "sources=$($srcList.Count)"
  foreach ($s in $srcList) {
    $name = [string]$s.name
    if (-not $name) { $name = [string]$s.source }
    $st = [string]$s.status
    if (-not $st) { $st = [string]$s.state }
    $detail = [string]$s.detail
    if (-not $detail) { $detail = [string]$s.last_error }
    $lvl = 'PASS'
    if ($st -match 'down|error|closed|unavailable') { $lvl = 'WARN' }
    Record $lvl "ops.source.$name" "status=$st detail=$(Clip $detail 100)"
  }
} else { Record 'FAIL' 'ops.status' "code=$($ops.code)" }

foreach ($path in @('environment', 'readiness', 'time', 'db', 'nats')) {
  $r = TryJson 'GET' "$Base/api/ops/$path" $Auth $null 20
  if ($r.ok) { Record 'PASS' "ops.$path" (Clip $r.body 120) }
  else { Record 'WARN' "ops.$path" "code=$($r.code) $(Clip $r.body 100)" }
}

# 4 datahub sources
$sources = TryJson 'GET' "$Base/api/datahub/sources" $Auth $null 30
if ($sources.ok) {
  $list = @()
  if ($sources.json.sources) { $list = @($sources.json.sources) }
  elseif ($sources.json.items) { $list = @($sources.json.items) }
  Record 'PASS' 'datahub.sources' "count=$($list.Count)"
  foreach ($s in $list) {
    $name = [string]$s.source
    if (-not $name) { $name = [string]$s.name }
    $st = [string]$s.state
    if (-not $st) { $st = [string]$s.status }
    $en = [string]$s.enabled
    $err = [string]$s.last_error
    $lvl = 'PASS'
    if ($st -match 'unavailable|closed|error') { $lvl = 'WARN' }
    Record $lvl "datahub.source.$name" "enabled=$en state=$st err=$(Clip $err 80)"
  }
} else { Record 'FAIL' 'datahub.sources' "code=$($sources.code) $(Clip $sources.body 160)" }

$catalog = TryJson 'GET' "$Base/api/datahub/source-catalog" $Auth $null 30
if ($catalog.ok) {
  $manifests = @()
  if ($catalog.json.sources) { $manifests = @($catalog.json.sources) }
  Record 'PASS' 'datahub.catalog' "manifests=$($manifests.Count)"
  foreach ($m in $manifests) {
    $id = [string]$m.id
    if (-not $id) { $id = [string]$m.source }
    Record 'PASS' "catalog.$id" (Clip ($m | ConvertTo-Json -Compress) 120)
  }
} else { Record 'WARN' 'datahub.catalog' "code=$($catalog.code) $(Clip $catalog.body 120)" }

$quality = TryJson 'GET' "$Base/api/datahub/quality" $Auth $null 30
if ($quality.ok) {
  $issues = @()
  if ($quality.json.issues) { $issues = @($quality.json.issues) }
  elseif ($quality.json.items) { $issues = @($quality.json.items) }
  Record 'PASS' 'datahub.quality' "issues=$($issues.Count)"
} else { Record 'WARN' 'datahub.quality' "code=$($quality.code)" }

$collect = TryJson 'POST' "$Base/api/datahub/collect/security" $Auth @{} 120
if ($collect.ok) { Record 'PASS' 'datahub.collect.security' (Clip $collect.body 200) }
else { Record 'WARN' 'datahub.collect.security' "code=$($collect.code) $(Clip $collect.body 200)" }

# 5 business reads
$groups = @(
  @{ name = 'market'; paths = @('/api/market/emotion', '/api/market/limit-up', '/api/market/watchlist') },
  @{ name = 'strategy.signals'; paths = @('/api/strategy/signals?limit=5') },
  @{ name = 'risk.state'; paths = @('/api/risk/state') },
  @{ name = 'trade.account'; paths = @('/api/account', '/api/trade/account', '/api/trade/positions') },
  @{ name = 'intel'; paths = @('/api/intel/feed?limit=5', '/api/intel/alerts?limit=5') },
  @{ name = 'brain.stats'; paths = @('/api/brain/stats') },
  @{ name = 'notify'; paths = @('/api/notify/messages?limit=5') },
  @{ name = 'backtest'; paths = @('/api/backtest/strategies?limit=5') },
  @{ name = 'config'; paths = @('/api/config') },
  @{ name = 'audit'; paths = @('/api/v1/audit?limit=5') }
)

foreach ($g in $groups) {
  $anyOk = $false
  $detail = ''
  foreach ($path in $g.paths) {
    $r = TryJson 'GET' "$Base$path" $Auth $null 25
    if ($r.ok) {
      $anyOk = $true
      $detail = "$path -> $($r.code) len=$($r.body.Length)"
      break
    }
    $detail += "$path->$($r.code); "
  }
  if ($anyOk) { Record 'PASS' $g.name $detail }
  else { Record 'WARN' $g.name $detail }
}

Write-Host ""
Write-Host "=== SUMMARY PASS=$($script:pass) WARN=$($script:warn) FAIL=$($script:fail) ==="
$reportDir = 'E:\project\NovaTrader\doc\开发文档\部署'
if (-not (Test-Path $reportDir)) { New-Item -ItemType Directory -Path $reportDir | Out-Null }
$report = Join-Path $reportDir ("44-api-test-{0:yyyyMMdd-HHmmss}.md" -f (Get-Date))
$md = @()
$md += '# NovaTrader 44 API Test Report'
$md += ''
$md += ("Time: {0}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
$md += "Base: $Base"
$md += "Web: $Web"
$md += ''
$md += "PASS=$($script:pass) WARN=$($script:warn) FAIL=$($script:fail)"
$md += ''
$md += '## Details'
$md += ''
$md += $results
$md -join "`n" | Set-Content -Path $report -Encoding UTF8
Write-Host "Report: $report"
if ($script:fail -gt 0) { exit 1 }
exit 0
