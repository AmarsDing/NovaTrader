# GNU ld/windres cannot open files under a non-ASCII path (this repo lives under 股票).
# Build artifacts and the Windows icon are kept on an ASCII path.
$ErrorActionPreference = "Stop"
$env:Path = "D:\Program Files\TDM-GCC-64\bin;$env:USERPROFILE\.cargo\bin;" + $env:Path
$env:CARGO_TARGET_DIR = "D:\nt-target"
New-Item -ItemType Directory -Force -Path C:\nt-icons | Out-Null
Copy-Item (Join-Path $PSScriptRoot "..\src-tauri\icons\icon.ico") "C:\nt-icons\icon.ico" -Force
$env:NOVATRADER_WIN_ICON = "C:\nt-icons\icon.ico"
Set-Location (Join-Path $PSScriptRoot "..")
npm run tauri dev
