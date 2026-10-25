$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $Root "pata2fla.exe"

if (-not (Test-Path $exe)) {
    Write-Host "[INFO] pata2fla.exe not found - running setup.bat first..." -ForegroundColor Yellow
    $env:PATA2FLA_NOPAUSE = "1"
    & cmd /c "`"$Root\setup.bat`""
}
if (-not (Test-Path $exe)) {
    Write-Host "[ERROR] pata2fla.exe was not built." -ForegroundColor Red
    exit 1
}
& $exe @args
exit $LASTEXITCODE
