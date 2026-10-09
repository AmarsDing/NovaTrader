# 拉起本机开发依赖：PostgreSQL、Redis、带 JetStream 的 NATS。
# 在仓库根目录执行：powershell -File backend/scripts/dev-up.ps1
$ErrorActionPreference = "Continue"

function Start-ServiceIfStopped($name) {
    $svc = Get-Service -Name $name -ErrorAction SilentlyContinue
    if ($null -eq $svc) { return }
    if ($svc.Status -ne "Running") {
        Start-Service -Name $name
        Write-Output "started $name"
    } else {
        Write-Output "$name already running"
    }
}

Start-ServiceIfStopped "postgresql-x64-18"
Start-ServiceIfStopped "Redis"

$nats = Get-CimInstance Win32_Process -Filter "Name = 'nats-server.exe'" -ErrorAction SilentlyContinue
if ($null -eq $nats) {
    $exe = "D:\nats\nats-server-v2.8.4-windows-amd64\nats-server.exe"
    New-Item -ItemType Directory -Force -Path "D:\nats\jetstream" | Out-Null
    Start-Process -FilePath $exe -ArgumentList "-js", "-sd", "D:\nats\jetstream"
    Write-Output "started nats-server -js"
} else {
    Write-Output "nats-server already running"
}

Write-Output "postgres 5432, redis 6379, nats 4222"
