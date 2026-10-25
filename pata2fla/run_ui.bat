@echo off
if not exist "pata2fla_gui.exe" (
    echo [INFO] Compiling pata2fla_ui.exe first...
    call setup_gui.bat
)
start pata2fla_gui.exe
