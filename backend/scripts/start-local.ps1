# Starts project processes only. PostgreSQL, Redis and NATS must already be available.
# Example: powershell -File backend/scripts/start-local.ps1
[CmdletBinding()]
param(
    [string]$BinaryDir = 'runtime/acceptance-2026-10-19',
    [string]$ConfigPath = 'configs/config.yaml',
    [string]$PythonPath = '',
    [string]$NodePath = '',
    [string]$DesktopExe = '',
    [ValidateRange(5, 120)][int]$StartupTimeoutSeconds = 30
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$backendRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$projectRoot = [IO.Path]::GetFullPath((Join-Path $backendRoot '..'))
$runtimeRoot = Join-Path $backendRoot 'runtime/local'
$manifestPath = Join-Path $runtimeRoot '.env.local-processes.json'
$lockPath = Join-Path $runtimeRoot '.env.local-processes.lock'
$utf8 = New-Object System.Text.UTF8Encoding($false)
$serviceNames = @('admin', 'datahub', 'market', 'intel', 'brain', 'strategy', 'risk', 'trade', 'backtest', 'notify')

function Resolve-LocalPath([string]$Value, [string]$Base) {
    if (-not [IO.Path]::IsPathRooted($Value)) { $Value = Join-Path $Base $Value }
    return [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $Value).Path)
}
function Same-Path([string]$Left, [string]$Right) {
    return [string]::Equals($Left, $Right, [StringComparison]::OrdinalIgnoreCase)
}
function Quote-Argument([string]$Value) {
    if ($Value.Contains('"') -or $Value.Contains("`r") -or $Value.Contains("`n")) { throw 'Unsupported argument character.' }
    return '"' + $Value + '"'
}
function Get-Identity([int]$ProcessId) {
    $process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    if ($null -eq $process) { return $null }
    $cim = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction Stop
    if ($null -eq $cim -or [string]::IsNullOrWhiteSpace($cim.ExecutablePath)) { throw "Cannot verify process identity: $ProcessId" }
    return [pscustomobject]@{
        pid = $ProcessId
        executable = [string]$cim.ExecutablePath
        started_utc_ticks = $process.StartTime.ToUniversalTime().Ticks.ToString()
        command_line = [string]$cim.CommandLine
    }
}
function Matches-Record($Identity, $Record) {
    return $null -ne $Identity -and (Same-Path $Identity.executable $Record.executable) -and
        $Identity.started_utc_ticks -eq [string]$Record.started_utc_ticks
}
function Matches-Spec($Identity, $Spec) {
    if ($null -eq $Identity -or -not (Same-Path $Identity.executable $Spec.executable)) { return $false }
    foreach ($marker in $Spec.markers) {
        if ($Identity.command_line.IndexOf($marker, [StringComparison]::OrdinalIgnoreCase) -lt 0) { return $false }
    }
    return $true
}
function Get-PortOwners([int[]]$Ports) {
    if ($Ports.Count -eq 0) { return @() }
    return @(Get-NetTCPConnection -State Listen -ErrorAction Stop |
        Where-Object { $_.LocalPort -in $Ports } | Select-Object -ExpandProperty OwningProcess -Unique)
}
function Get-ServicePorts([string]$Yaml, [string]$Service) {
    $section = [regex]::Match($Yaml, '(?ms)^' + [regex]::Escape($Service) + ':\s*\r?\n(?<body>.*?)(?=^\S|\z)')
    if (-not $section.Success) { throw "Missing service configuration: $Service" }
    $matches = [regex]::Matches($section.Groups['body'].Value, '(?m)^  (?:http|grpc|websocket):\s*\r?\n    addr:\s*[^\r\n]*:(?<port>\d+)\s*$')
    $ports = @($matches | ForEach-Object { [int]$_.Groups['port'].Value })
    if ($ports.Count -lt 2 -or @($ports | Where-Object { $_ -lt 1 -or $_ -gt 65535 }).Count -gt 0) {
        throw "Unsupported service port configuration: $Service"
    }
    return $ports
}
function Save-Manifest {
    $manifest = [ordered]@{ schema_version = 1; project_root = $projectRoot; updated_at = [DateTime]::UtcNow.ToString('o'); processes = @($script:records) }
    $temporaryPath = Join-Path $runtimeRoot '.env.local-processes.pending.json'
    [IO.File]::WriteAllText($temporaryPath, ($manifest | ConvertTo-Json -Depth 8), $utf8)
    if (Test-Path -LiteralPath $manifestPath) { [IO.File]::Replace($temporaryPath, $manifestPath, $null) }
    else { [IO.File]::Move($temporaryPath, $manifestPath) }
}
function Record-Process($Spec, $Identity, [bool]$Owned) {
    $script:records = @($script:records | Where-Object { $_.name -ne $Spec.name })
    $script:records += [pscustomobject]@{
        name = $Spec.name; pid = $Identity.pid; executable = $Identity.executable
        started_utc_ticks = $Identity.started_utc_ticks; owned = $Owned
        ports = @($Spec.ports); markers = @($Spec.markers); log_directory = $Spec.log_directory
    }
    Save-Manifest
}
function Wait-ProcessPorts($Spec, $Identity) {
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    do {
        if (-not (Matches-Record (Get-Identity $Identity.pid) $Identity)) { throw "$($Spec.name) exited during startup. See $($Spec.log_directory)" }
        $listeners = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { $_.LocalPort -in $Spec.ports })
        $wrong = @($listeners | Where-Object { $_.OwningProcess -ne $Identity.pid })
        if ($wrong.Count -gt 0) { throw "$($Spec.name) has a competing port owner; no other process was stopped." }
        $listeningPorts = @($listeners | Select-Object -ExpandProperty LocalPort -Unique)
        if (@($Spec.ports | Where-Object { $_ -notin $listeningPorts }).Count -eq 0) { return }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$($Spec.name) did not listen on all expected ports. Process retained for diagnosis: $($Spec.log_directory)"
}

$resolvedBinaryDir = Resolve-LocalPath $BinaryDir $backendRoot
if (-not $resolvedBinaryDir.StartsWith($backendRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'BinaryDir must be inside this project backend.' }
$resolvedConfigPath = Resolve-LocalPath $ConfigPath $backendRoot
$yaml = [IO.File]::ReadAllText($resolvedConfigPath)
# This constrained replacement changes exactly log.file.filename and preserves the rest byte-for-byte after UTF-8 decoding.
$logPattern = '(?ms)(^log:\s*\r?\n(?:(?:[ \t]+[^\r\n]*|[ \t]*)\r?\n)*?^  file:\s*\r?\n(?:(?:[ \t]{4}[^\r\n]*|[ \t]*)\r?\n)*?^    filename:[ \t]*)[^\r\n]*'
$logRegex = New-Object Text.RegularExpressions.Regex($logPattern)
if ($logRegex.Matches($yaml).Count -ne 1) { throw 'Expected exactly one log.file.filename in the source YAML.' }
if ([string]::IsNullOrWhiteSpace($PythonPath)) { $PythonPath = Join-Path $projectRoot 'pyworker/.venv/Scripts/python.exe' }
if ([string]::IsNullOrWhiteSpace($NodePath)) { $NodePath = (Get-Command node.exe -ErrorAction Stop).Source }
$pythonExe = Resolve-LocalPath $PythonPath $projectRoot
$nodeExe = Resolve-LocalPath $NodePath $projectRoot
$workerScript = Resolve-LocalPath 'pyworker/app.py' $projectRoot
$viteScript = Resolve-LocalPath 'web/node_modules/vite/bin/vite.js' $projectRoot
$workerPort = 18080
if (-not [string]::IsNullOrWhiteSpace($env:PYWORKER_LISTEN)) {
    if ($env:PYWORKER_LISTEN -notmatch '^(127\.0\.0\.1|localhost):(\d+)$') { throw 'PYWORKER_LISTEN must be a loopback host:port.' }
    $workerPort = [int]$Matches[2]
    if ($workerPort -lt 1 -or $workerPort -gt 65535) { throw 'Invalid PYWORKER_LISTEN port.' }
}

$specs = @()
foreach ($name in $serviceNames) {
    $exe = Resolve-LocalPath ($name + '.exe') $resolvedBinaryDir
    $config = Join-Path $runtimeRoot ('.env.' + $name + '.yaml')
    $specs += [pscustomobject]@{
        name = $name; executable = $exe; arguments = @('-conf', $config)
        markers = @(); working_directory = $backendRoot; ports = @(Get-ServicePorts $yaml $name)
        config_path = $config; log_directory = (Join-Path $runtimeRoot $name); visible = $false
    }
}
$specs = @([pscustomobject]@{
    name = 'pyworker'; executable = $pythonExe; arguments = @('-u', $workerScript); markers = @($workerScript)
    working_directory = (Join-Path $projectRoot 'pyworker'); ports = @($workerPort)
    config_path = $null; log_directory = (Join-Path $runtimeRoot 'pyworker'); visible = $false
}) + $specs
$specs += [pscustomobject]@{
    name = 'vite'; executable = $nodeExe; arguments = @($viteScript, '--host', '127.0.0.1', '--port', '5173', '--strictPort')
    markers = @($viteScript); working_directory = (Join-Path $projectRoot 'web'); ports = @(5173)
    config_path = $null; log_directory = (Join-Path $runtimeRoot 'vite'); visible = $false
}
if (-not [string]::IsNullOrWhiteSpace($DesktopExe)) {
    $desktopPath = Resolve-LocalPath $DesktopExe $projectRoot
    if ([IO.Path]::GetExtension($desktopPath) -ne '.exe') { throw 'DesktopExe must identify a prebuilt desktop executable.' }
    $specs += [pscustomobject]@{
        name = 'desktop'; executable = $desktopPath; arguments = @(); markers = @()
        working_directory = $projectRoot; ports = @(); config_path = $null
        log_directory = (Join-Path $runtimeRoot 'desktop'); visible = $true
    }
}
if (@($specs.ports | Group-Object | Where-Object { $_.Count -gt 1 }).Count -gt 0) { throw 'Configured application ports overlap.' }
New-Item -ItemType Directory -Force -Path $runtimeRoot | Out-Null
$lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
try {
    $script:records = @()
    if (Test-Path -LiteralPath $manifestPath) {
        $previous = [IO.File]::ReadAllText($manifestPath) | ConvertFrom-Json
        if ($previous.schema_version -ne 1 -or -not (Same-Path $previous.project_root $projectRoot)) { throw 'Process manifest belongs to another project or version.' }
        $script:records = @($previous.processes)
    }
    # Validate every occupied application port before starting any new process.
    foreach ($spec in $specs) {
        foreach ($owner in @(Get-PortOwners $spec.ports)) {
            if (-not (Matches-Spec (Get-Identity $owner) $spec)) { throw "Port for $($spec.name) is occupied by a different or unverifiable process (PID $owner)." }
        }
    }
    foreach ($spec in $specs) {
        New-Item -ItemType Directory -Force -Path $spec.log_directory | Out-Null
        $saved = @($script:records | Where-Object { $_.name -eq $spec.name })
        if ($saved.Count -gt 1) { throw "Duplicate process manifest entry: $($spec.name)" }
        if ($saved.Count -eq 1) {
            $identity = Get-Identity $saved[0].pid
            if ((Matches-Record $identity $saved[0]) -and (Matches-Spec $identity $spec)) {
                Wait-ProcessPorts $spec $identity
                Write-Output "reused $($spec.name) PID=$($identity.pid) owned=$($saved[0].owned)"
                continue
            }
        }
        $owners = @(Get-PortOwners $spec.ports)
        if ($owners.Count -gt 1) { throw "Multiple processes own $($spec.name) ports; refusing to add an instance." }
        if ($owners.Count -eq 1) {
            $identity = Get-Identity $owners[0]
            if (-not (Matches-Spec $identity $spec)) { throw "Port ownership changed for $($spec.name)." }
            Wait-ProcessPorts $spec $identity
            Record-Process $spec $identity $false
            Write-Output "reused external project process $($spec.name) PID=$($identity.pid); stop-local will not stop it"
            continue
        }
        # Desktop has no listening port; do not open a second copy of the selected executable.
        if ($spec.name -eq 'desktop') {
            $existing = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and (Same-Path $_.ExecutablePath $spec.executable) })
            if ($existing.Count -gt 0) {
                Record-Process $spec (Get-Identity $existing[0].ProcessId) $false
                Write-Output 'reused existing desktop; stop-local will not stop it'
                continue
            }
        }
        if ($null -ne $spec.config_path) {
            $replacement = '${1}' + ('./runtime/local/' + $spec.name + '/server.log')
            [IO.File]::WriteAllText($spec.config_path, $logRegex.Replace($yaml, $replacement, 1), $utf8)
        }
        $launch = @{
            FilePath = $spec.executable; WorkingDirectory = $spec.working_directory; PassThru = $true
            RedirectStandardOutput = (Join-Path $spec.log_directory 'stdout.log')
            RedirectStandardError = (Join-Path $spec.log_directory 'stderr.log')
            WindowStyle = 'Hidden'
        }
        if ($spec.arguments.Count -gt 0) { $launch.ArgumentList = (($spec.arguments | ForEach-Object { Quote-Argument $_ }) -join ' ') }
        if ($spec.visible) { $launch.WindowStyle = 'Normal' }
        $process = Start-Process @launch
        $identity = Get-Identity $process.Id
        if (-not (Matches-Spec $identity $spec)) { throw "Started process identity did not match $($spec.name); inspect PID $($process.Id)." }
        Record-Process $spec $identity $true
        Wait-ProcessPorts $spec $identity
        Write-Output "started $($spec.name) PID=$($identity.pid)"
    }
    Write-Output "Application processes are listening; business readiness requires acceptance checks. Manifest: $manifestPath"
} finally {
    $lock.Dispose()
}
