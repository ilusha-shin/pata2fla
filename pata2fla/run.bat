@echo off
setlocal
set "ROOT=%~dp0"

if exist "%ROOT%pata2fla.exe" goto RUN

echo [INFO] pata2fla.exe not found - running setup_ui.bat first...
set "PATA2FLA_NOPAUSE=1"
call "%ROOT%setup_ui.bat"
if not exist "%ROOT%pata2fla.exe" (
    echo [ERROR] Build failed - pata2fla.exe was not created.
    echo Rebuild manually: setup_ui.bat
    exit /b 1
)

:RUN
REM CLI binary must be console subsystem (not a copy of pata2fla_ui.exe).
echo [run.bat] launching CLI: "%ROOT%pata2fla.exe"
echo [run.bat] args: %*
"%ROOT%pata2fla.exe" --cli %*
set "EC=%ERRORLEVEL%"
if not "%EC%"=="0" echo [run.bat] exit code %EC%
exit /b %EC%
