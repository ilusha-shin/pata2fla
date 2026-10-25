# Thin wrapper: the real logic lives in setup.bat so both entry points behave identically.
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
& cmd /c "`"$Root\setup.bat`""
exit $LASTEXITCODE
