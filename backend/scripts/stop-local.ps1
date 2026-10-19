# Stops only processes created and recorded by start-local.ps1; never infrastructure services.
[CmdletBinding(SupportsShouldProcess = $true)]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$backendRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$projectRoot = [IO.Path]::GetFullPath((Join-Path $backendRoot '..'))
$runtimeRoot = Join-Path $backendRoot 'runtime/local'
$manifestPath = Join-Path $runtimeRoot '.env.local-processes.json'
$lockPath = Join-Path $runtimeRoot '.env.local-processes.lock'
$allowedNames = @('admin', 'datahub', 'market', 'intel', 'brain', 'strategy', 'risk', 'trade', 'backtest', 'notify', 'pyworker', 'vite', 'desktop')
if (-not (Test-Path -LiteralPath $manifestPath)) { Write-Output 'No managed local processes.'; return }
$lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
try {
    $manifest = [IO.File]::ReadAllText($manifestPath) | ConvertFrom-Json
    if ($manifest.schema_version -ne 1 -or -not [string]::Equals($manifest.project_root, $projectRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Process manifest belongs to another project or version.'
    }
    $records = @($manifest.processes)
    [array]::Reverse($records)
    foreach ($record in $records) {
        if ($record.name -notin $allowedNames) { throw "Unexpected manifest process name: $($record.name)" }
        if ($record.owned -ne $true) { Write-Output "kept externally started $($record.name)"; continue }
        if ($record.name -notin @('pyworker', 'vite', 'desktop')) {
            $expectedLeaf = $record.name + '.exe'
            if ([IO.Path]::GetFileName($record.executable) -ne $expectedLeaf -or
                -not $record.executable.StartsWith($backendRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
                throw "Executable outside project boundary: $($record.name)"
            }
        }
        if ($record.name -eq 'pyworker' -and @( $record.markers | Where-Object { $_ -eq (Join-Path $projectRoot 'pyworker/app.py') }).Count -ne 1) { throw 'Invalid pyworker identity marker.' }
        if ($record.name -eq 'vite' -and @( $record.markers | Where-Object { $_ -eq (Join-Path $projectRoot 'web/node_modules/vite/bin/vite.js') }).Count -ne 1) { throw 'Invalid Vite identity marker.' }
        $process = Get-Process -Id $record.pid -ErrorAction SilentlyContinue
        if ($null -eq $process) { Write-Output "already exited $($record.name)"; continue }
        $cim = Get-CimInstance Win32_Process -Filter "ProcessId = $($record.pid)" -ErrorAction Stop
        if ($null -eq $cim -or -not [string]::Equals($cim.ExecutablePath, $record.executable, [StringComparison]::OrdinalIgnoreCase) -or
            $process.StartTime.ToUniversalTime().Ticks.ToString() -ne [string]$record.started_utc_ticks) {
            Write-Warning "Identity mismatch for $($record.name) PID=$($record.pid); kept process."
            continue
        }
        $validMarkers = $true
        foreach ($marker in $record.markers) {
            if ([string]$cim.CommandLine -eq '' -or $cim.CommandLine.IndexOf($marker, [StringComparison]::OrdinalIgnoreCase) -lt 0) { $validMarkers = $false }
        }
        if (-not $validMarkers) { Write-Warning "Command identity mismatch for $($record.name); kept process."; continue }
        if ($PSCmdlet.ShouldProcess("$($record.name) PID=$($record.pid)", 'Stop recorded project process')) {
            # Kill through this Process handle only after validating its start time; never by name or process tree.
            $null = $process.Handle
            if ($process.HasExited -or $process.StartTime.ToUniversalTime().Ticks.ToString() -ne [string]$record.started_utc_ticks) { continue }
            $process.Kill()
            $null = $process.WaitForExit(5000)
            Write-Output "stopped $($record.name) PID=$($record.pid)"
        }
    }
    Write-Output 'Manifest retained for identity checks and restart; PostgreSQL, Redis, NATS and unowned processes were not stopped.'
} finally {
    $lock.Dispose()
}
