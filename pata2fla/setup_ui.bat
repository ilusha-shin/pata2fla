@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

echo ===================================================================
echo    PATA2FLA (DRUM AI TO CLONE HERO) -- GIOUI EDITION (PURE GO, ZERO GCC/CGO)
echo ===================================================================

where go >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go compiler not found in PATH!
    echo Download and install Go from: https://go.dev/dl/
if not defined PATA2FLA_NOPAUSE pause
    exit /b 1
)

if not exist "models" mkdir "models"
if not exist "input" mkdir "input"
if not exist "output" mkdir "output"
if not exist "assets" mkdir "assets"

echo.
echo --- [Step 1/4] Go version and modules ---
go version
echo Resolving Go modules and generating go.sum (go mod tidy)...
set CGO_ENABLED=0
go mod tidy
if %ERRORLEVEL% neq 0 (
    echo [ERROR] "go mod tidy" failed. Make sure you have Internet access to download packages.
if not defined PATA2FLA_NOPAUSE pause
    exit /b 1
)

echo.
echo --- [Step 2/4] onnxruntime.dll (required, v1.24.x) ---
if exist "onnxruntime.dll" goto ORT_OK

echo [>>] Downloading onnxruntime 1.24.1 for Windows x64 (~60 MB)...
if exist "_ort_tmp" rmdir /s /q "_ort_tmp"
if exist "ort.zip" del /q "ort.zip" >nul 2>nul

where curl.exe >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    curl.exe -L --fail --retry 5 --retry-delay 3 -o "ort.zip" "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-win-x64-1.24.1.zip"
) else (
    powershell -NoProfile -Command "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; Invoke-WebRequest -Uri 'https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-win-x64-1.24.1.zip' -OutFile 'ort.zip'"
)
if not exist "ort.zip" goto ERR_ORT

powershell -NoProfile -Command "Expand-Archive -Force -Path 'ort.zip' -DestinationPath '_ort_tmp'"
if not exist "_ort_tmp\onnxruntime-win-x64-1.24.1\lib\onnxruntime.dll" goto ERR_ORT

copy /y "_ort_tmp\onnxruntime-win-x64-1.24.1\lib\onnxruntime.dll" "onnxruntime.dll" >nul
if not exist "onnxruntime.dll" goto ERR_ORT

rmdir /s /q "_ort_tmp"
del /q "ort.zip" >nul 2>nul

:ORT_OK
echo    [OK] onnxruntime.dll ready

echo.
echo --- [Step 3/4] Checking and downloading models in models/ ---
if exist "models\stemsplit_drums.onnx" goto STEMSPLIT_OK
if exist "models\htdemucs_ft_drums.onnx" goto STEMSPLIT_OK
if exist "models\stemsplitio.onnx" goto STEMSPLIT_OK

echo Downloading stemsplit_drums.onnx [HT-Demucs FT drums] ...
where curl.exe >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    curl.exe -fSL --retry 5 --retry-delay 3 -o "models\stemsplit_drums.onnx" "https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx"
) else (
    powershell -NoProfile -Command "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; (New-Object System.Net.WebClient).DownloadFile('https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx', 'models\stemsplit_drums.onnx')"
)

:STEMSPLIT_OK
if exist "models\stemsplit_drums.onnx" (
    echo   [OK] stemsplit_drums.onnx is ready
) else if exist "models\htdemucs_ft_drums.onnx" (
    echo   [OK] htdemucs_ft_drums.onnx is ready
) else if exist "models\stemsplitio.onnx" (
    echo   [OK] stemsplitio.onnx is ready
) else (
    echo   [WARNING] stemsplit_drums.onnx download failed.
    echo   You can download manually from:
    echo   https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx
)

if not exist "models\adtof.onnx" (
    where python >nul 2>nul
    if %ERRORLEVEL% EQU 0 (
        if exist "export_adtof_to_onnx.py" (
            echo [INFO] adtof.onnx missing. Attempting export via python...
            python export_adtof_to_onnx.py --out "models\adtof.onnx" >nul 2>nul
        )
    )
)

echo.
echo --- [Step 4/4] Compiling cmd/pata2fla (CLI) and cmd/pata2fla_ui (GUI) ---
set CGO_ENABLED=0

REM CLI: console subsystem for full DECISION TRACE logs
go build -ldflags="-s -w" -o pata2fla.exe ./cmd/pata2fla_cli
if %ERRORLEVEL% neq 0 (
    echo [ERROR] CLI compilation failed!
if not defined PATA2FLA_NOPAUSE pause
    exit /b 1
)
echo   [OK] pata2fla.exe (CLI) compiled from ./cmd/pata2fla_cli

REM GUI: no console window (-H windowsgui). Logs go to the in-app console panel.
go build -ldflags="-H windowsgui -s -w" -o pata2fla_gui.exe ./cmd/pata2fla_gui
if %ERRORLEVEL% neq 0 (
    echo [INFO] Retrying GUI build without -H windowsgui...
    go build -ldflags="-s -w" -o pata2fla_gui.exe ./cmd/pata2fla_gui
)
if %ERRORLEVEL% neq 0 (
    echo.
    echo [ERROR] GUI compilation failed!
    echo If missing modules, run: go mod tidy
if not defined PATA2FLA_NOPAUSE pause
    exit /b 1
)
echo   [OK] pata2fla_gui.exe (GUI) compiled from ./cmd/pata2fla_gui

echo.
echo ===================================================================
echo   Runtime Status:
echo ===================================================================
if exist "onnxruntime.dll" (
    echo    [+] onnxruntime.dll       [READY]
) else (
    echo    [-] onnxruntime.dll       [MISSING]
)

echo.
echo ===================================================================
echo   Models Status:
echo ===================================================================
if exist "models\stemsplit_drums.onnx" (
    echo    [+] stemsplit_drums.onnx  [READY]
) else if exist "models\htdemucs_ft_drums.onnx" (
    echo    [+] htdemucs_ft_drums.onnx [READY]
) else if exist "models\stemsplitio.onnx" (
    echo    [+] stemsplitio.onnx      [READY]
) else (
    echo    [-] stemsplit_drums.onnx  [MISSING]
)
if exist "models\adtof.onnx" (
    echo    [+] adtof.onnx            [READY]
) else (
    echo    [-] adtof.onnx            [OPTIONAL / ACOUSTIC FALLBACK]
)
if exist "models\hh_best.onnx" (
    echo    [+] hh_best.onnx          [READY]
) else (
    echo    [?] hh_best.onnx          [OPTIONAL: place in models folder]
)
if exist "models\cy_best.onnx" (
    echo    [+] cy_best.onnx          [READY]
) else (
    echo    [?] cy_best.onnx          [OPTIONAL: place in models folder]
)
if exist "models\tt_best.onnx" (
    echo    [+] tt_best.onnx          [READY]
) else (
    echo    [?] tt_best.onnx          [OPTIONAL: place in models folder]
)
if exist "models\cy_arbiter.onnx" (
    echo    [+] cy_arbiter.onnx       [READY]
) else (
    echo    [?] cy_arbiter.onnx       [OPTIONAL: place in models folder]
)

echo.
echo ===================================================================
echo   Header Assets Status (assets/):
echo ===================================================================
if exist "assets\background.png" (
    echo    [+] assets\background.png     [CUSTOM BACKGROUND READY]
) else (
    echo    [-] assets\background.png     [OPTIONAL: place background here]
)
if exist "assets\IconPata2Fla.png" (
    echo    [+] assets\IconPata2Fla.png   [ICON READY]
) else (
    echo    [-] assets\IconPata2Fla.png   [OPTIONAL: place icon here]
)
if exist "assets\logo.png" (
    echo    [+] assets\logo.png            [LOGO READY]
) else (
    echo    [-] assets\logo.png            [OPTIONAL: place logo here]
)
if exist "assets\logo1.png" (
    echo    [+] assets\logo1.png           [LOGO1 READY]
) else (
    echo    [-] assets\logo1.png           [OPTIONAL: place logo1 here]
)

echo.
echo ===================================================================
echo   SETUP COMPLETED SUCCESSFULLY!
echo ===================================================================
echo Launch GUI:
echo    Double-click pata2fla_gui.exe or run_ui.bat
echo Launch CLI:
echo    pata2fla.exe -i "input\track.flac" -o "output" --bpm 120 --quantize 24
echo    pata2fla.exe --drums-only -i "stem.wav" -o "output"
echo ===================================================================
echo.
if not defined PATA2FLA_NOPAUSE pause
exit /b 0

:ERR_ORT
echo.
echo [ERROR] Failed to download or extract onnxruntime.dll
echo Manual steps:
echo   1. Open https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-win-x64-1.24.1.zip
echo   2. Extract lib\onnxruntime.dll next to this script / pata2fla_gui.exe
if exist "ort.zip" del /q "ort.zip" >nul 2>nul
if exist "_ort_tmp" rmdir /s /q "_ort_tmp"
if not defined PATA2FLA_NOPAUSE pause
exit /b 1
