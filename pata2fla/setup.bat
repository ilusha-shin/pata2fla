@echo off
setlocal
cd /d "%~dp0"
echo ===================================================================
echo   PATA2FLA (STANDALONE GO EDITION) -- SETUP AND BUILD
echo ===================================================================

where go >nul 2>nul
if errorlevel 1 goto ERR_GO

if not exist "models" mkdir "models"
if not exist "output" mkdir "output"
if not exist "input" mkdir "input"

echo.
echo [1/5] Checking Go version:
go version
set "CGO_ENABLED=0"

echo.
echo [2/5] Downloading Go modules and onnxruntime.dll...
go mod tidy
if errorlevel 1 goto ERR_TIDY
if exist "onnxruntime.dll" goto ORT_OK
echo [>>] Downloading onnxruntime 1.24.1 for Windows x64 (about 60 MB)...
if exist "_ort_tmp" rmdir /s /q "_ort_tmp"
curl.exe -L --fail --retry 5 --retry-delay 3 -o "ort.zip" "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-win-x64-1.24.1.zip"
if errorlevel 1 goto ERR_ORT
powershell -NoProfile -Command "Expand-Archive -Force -Path 'ort.zip' -DestinationPath '_ort_tmp'"
if not exist "_ort_tmp\onnxruntime-win-x64-1.24.1\lib\onnxruntime.dll" goto ERR_ORT
copy /y "_ort_tmp\onnxruntime-win-x64-1.24.1\lib\onnxruntime.dll" "onnxruntime.dll" >nul
rmdir /s /q "_ort_tmp"
del /q "ort.zip" >nul 2>nul
:ORT_OK
echo    [OK] onnxruntime.dll ready

echo.
echo [3/5] Downloading models...

:: ---- stemsplit_drums.onnx (HT-Demucs FT drums, about 316 MB) ----
if exist "models\htdemucs_ft_drums.onnx" goto MODEL_1_CHECK_SIZE
if exist "models\stemsplitio.onnx" goto MODEL_1_CHECK_SIZE
if not exist "models\stemsplit_drums.onnx" goto MODEL_1_DOWNLOAD
:MODEL_1_CHECK_SIZE
set "MSZ=0"
for %%A in ("models\stemsplit_drums.onnx" "models\htdemucs_ft_drums.onnx" "models\stemsplitio.onnx") do if exist "%%~A" set "MSZ=%%~zA"
if %MSZ% LSS 100000000 goto MODEL_1_BROKEN
echo    [OK] drum separation model already exists
goto MODEL_1_DONE

:MODEL_1_BROKEN
echo    [!] drum separation model is incomplete, downloading again...
del /q "models\stemsplit_drums.onnx" >nul 2>nul
del /q "models\htdemucs_ft_drums.onnx" >nul 2>nul
del /q "models\stemsplitio.onnx" >nul 2>nul

:MODEL_1_DOWNLOAD
echo [>>] Downloading stemsplit_drums.onnx (316 MB, please wait)...
where curl.exe >nul 2>nul
if errorlevel 1 goto DOWNLOAD_PS_1
curl.exe -L --fail --retry 5 --retry-delay 3 -C - --progress-bar -o "models\stemsplit_drums.onnx.part" "https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx"
goto MODEL_1_VERIFY

:DOWNLOAD_PS_1
powershell -NoProfile -Command "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; (New-Object System.Net.WebClient).DownloadFile('https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx', 'models\stemsplit_drums.onnx.part')"

:MODEL_1_VERIFY
set "MSZ=0"
for %%A in ("models\stemsplit_drums.onnx.part") do set "MSZ=%%~zA"
if %MSZ% LSS 100000000 goto MODEL_1_FAILED
move /y "models\stemsplit_drums.onnx.part" "models\stemsplit_drums.onnx" >nul
echo    [OK] stemsplit_drums.onnx downloaded
goto MODEL_1_DONE

:MODEL_1_FAILED
echo    [ERROR] Download of stemsplit_drums.onnx failed or is incomplete.
echo            Run setup.bat again to resume, or download it manually from
echo            https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx
echo            and save it as models\stemsplit_drums.onnx

:MODEL_1_DONE

:: ---- adtof.onnx (created once from the PyTorch weights; Python is NOT needed afterwards) ----
if exist "models\adtof.onnx" goto MODEL_2_OK
echo [>>] adtof.onnx is missing - one-time export from adtof_pytorch...
where python >nul 2>nul
if errorlevel 1 goto MODEL_2_MISSING
if not exist "export_adtof_to_onnx.py" goto MODEL_2_MISSING
python export_adtof_from_local_pytorch_to_onnx.py --out models\adtof.onnx
if exist "models\adtof.onnx" goto MODEL_2_OK
:MODEL_2_MISSING
echo    [!] adtof.onnx was NOT created. Put your own ADTOF ONNX file at models\adtof.onnx
echo        or install: pip install torch onnx git+https://github.com/xavriley/ADTOF-pytorch.git
echo        and run setup.bat again. (Without it the pipeline stops, unless --allow-fallback)
goto MODEL_2_DONE
:MODEL_2_OK
echo    [OK] adtof.onnx is ready
:MODEL_2_DONE

echo.
echo [4/5] Building pata2fla.exe 
go build -ldflags="-s -w" -o pata2fla.exe ./cmd/pata2fla_cli
if %ERRORLEVEL% neq 0 (
    echo [ERROR] CLI compilation failed!
if not defined PATA2FLA_NOPAUSE pause
    exit /b 1
)

echo.
echo [5/5] Models status:
if exist "models\stemsplit_drums.onnx" (echo    [+] stemsplit_drums.onnx  [READY]) else (echo    [-] stemsplit_drums.onnx  [MISSING])
if exist "models\adtof.onnx"            (echo    [+] adtof.onnx            [READY]) else (echo    [-] adtof.onnx            [MISSING - REQUIRED])
if exist "models\hh_best.onnx"          (echo    [+] hh_best.onnx          [READY]) else (echo    [?] hh_best.onnx          [OPTIONAL - PLACE IN models])
if exist "models\cy_best.onnx"          (echo    [+] cy_best.onnx          [READY]) else (echo    [?] cy_best.onnx          [OPTIONAL - PLACE IN models])
if exist "models\tt_best.onnx"          (echo    [+] tt_best.onnx          [READY]) else (echo    [?] tt_best.onnx          [OPTIONAL - PLACE IN models])
if exist "models\cy_arbiter.onnx"       (echo    [+] cy_arbiter.onnx       [READY]) else (echo    [?] cy_arbiter.onnx       [OPTIONAL - PLACE IN models])

echo ===================================================================
echo [SUCCESS] Setup complete!
echo Run transcription:
echo    run.bat --input="path_to_your_song.flac" --out="path_to_output_folder"
echo ===================================================================
if not defined PATA2FLA_NOPAUSE pause
exit /b 0

:ERR_GO
echo [ERROR] Go compiler not found!
echo Download and install Go from: https://go.dev/dl/
if not defined PATA2FLA_NOPAUSE pause
exit /b 1

:ERR_TIDY
echo [ERROR] "go mod tidy" failed (no internet access to the Go module proxy?).
if not defined PATA2FLA_NOPAUSE pause
exit /b 1

:ERR_ORT
echo [ERROR] Could not download or unpack onnxruntime 1.24.1.
echo         Download onnxruntime-win-x64-1.24.1.zip from https://github.com/microsoft/onnxruntime/releases/tag/v1.24.1
echo         and copy lib\onnxruntime.dll next to this script.
if not defined PATA2FLA_NOPAUSE pause
exit /b 1

:ERR_GO_BUILD
echo.
echo [ERROR] Failed to compile Go project!
if not defined PATA2FLA_NOPAUSE pause
exit /b 1
