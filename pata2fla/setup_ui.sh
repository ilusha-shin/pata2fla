#!/usr/bin/env bash
# PATA2FLA UI setup for Linux / macOS (Pure Go, CGO_ENABLED=0)
set -u
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

echo "==================================================================="
echo "   PATA2FLA (DRUM AI TO CLONE HERO) -- GIOUI EDITION"
echo "   Pure Go, zero GCC/CGO"
echo "==================================================================="

if ! command -v go >/dev/null 2>&1; then
    echo "[ERROR] Go compiler not found in PATH!"
    echo "Install: https://go.dev/dl/  (or: brew install go / sudo apt install golang-go)"
    exit 1
fi

mkdir -p models input output assets

echo ""
echo "--- [Step 1/4] Go version and modules ---"
go version
export CGO_ENABLED=0
echo "Resolving Go modules (go mod tidy)..."
if ! go mod tidy; then
    echo "[ERROR] go mod tidy failed. Check Internet access."
    exit 1
fi

echo ""
echo "--- [Step 2/4] onnxruntime shared library (required, v1.24.x) ---"
VER=1.24.1
OS="$(uname -s)"
ARCH="$(uname -m)"
NEED_DOWNLOAD=1

case "$OS" in
    Linux)
        OUT="libonnxruntime.so"
        [ -f "$OUT" ] && NEED_DOWNLOAD=0
        case "$ARCH" in
            x86_64|amd64)  PKG="onnxruntime-linux-x64-$VER";    LIBF="libonnxruntime.so.$VER" ;;
            aarch64|arm64) PKG="onnxruntime-linux-aarch64-$VER"; LIBF="libonnxruntime.so.$VER" ;;
            *) PKG="" ;;
        esac
        ;;
    Darwin)
        OUT="libonnxruntime.dylib"
        [ -f "$OUT" ] && NEED_DOWNLOAD=0
        case "$ARCH" in
            arm64)  PKG="onnxruntime-osx-arm64-$VER";  LIBF="libonnxruntime.$VER.dylib" ;;
            x86_64) PKG="onnxruntime-osx-x86_64-$VER"; LIBF="libonnxruntime.$VER.dylib" ;;
            *) PKG="" ;;
        esac
        ;;
    *)
        echo "[ERROR] Unsupported OS: $OS"
        exit 1
        ;;
esac

if [ "$NEED_DOWNLOAD" -eq 1 ]; then
    if [ -z "${PKG:-}" ]; then
        echo "[ERROR] Unsupported arch: $OS / $ARCH"
        echo "Download onnxruntime $VER from https://github.com/microsoft/onnxruntime/releases/tag/v$VER"
        echo "and place $OUT next to this script."
        exit 1
    fi
    echo "[>>] Downloading onnxruntime $VER ($PKG)..."
    TMP_TGZ="${TMPDIR:-/tmp}/ort-pata2fla.tgz"
    TMP_DIR="${TMPDIR:-/tmp}/ort-pata2fla-extract"
    rm -rf "$TMP_DIR"
    mkdir -p "$TMP_DIR"
    URL="https://github.com/microsoft/onnxruntime/releases/download/v$VER/$PKG.tgz"
    if command -v curl >/dev/null 2>&1; then
        curl -L --fail --retry 5 --retry-delay 3 -o "$TMP_TGZ" "$URL" || {
            echo "[ERROR] curl download failed: $URL"
            exit 1
        }
    elif command -v wget >/dev/null 2>&1; then
        wget -O "$TMP_TGZ" "$URL" || {
            echo "[ERROR] wget download failed: $URL"
            exit 1
        }
    else
        echo "[ERROR] neither curl nor wget found"
        exit 1
    fi
    if ! tar -xzf "$TMP_TGZ" -C "$TMP_DIR"; then
        echo "[ERROR] failed to extract $TMP_TGZ"
        exit 1
    fi
    SRC="$TMP_DIR/$PKG/lib/$LIBF"
    if [ ! -f "$SRC" ]; then
        # some packages nest versioned name only under lib/
        SRC="$(find "$TMP_DIR/$PKG/lib" -maxdepth 1 -type f \( -name 'libonnxruntime.so*' -o -name 'libonnxruntime*.dylib' \) | head -1 || true)"
    fi
    if [ -z "${SRC:-}" ] || [ ! -f "$SRC" ]; then
        echo "[ERROR] library not found inside package $PKG"
        exit 1
    fi
    cp -f "$SRC" "./$OUT"
    rm -f "$TMP_TGZ"
    rm -rf "$TMP_DIR"
fi

if [ ! -f libonnxruntime.so ] && [ ! -f libonnxruntime.dylib ]; then
    echo "[ERROR] onnxruntime $VER shared library missing."
    echo "Download from https://github.com/microsoft/onnxruntime/releases/tag/v$VER"
    echo "and copy as libonnxruntime.so (Linux) or libonnxruntime.dylib (macOS) next to this script."
    exit 1
fi
echo "   [OK] onnxruntime library ready"

echo ""
echo "--- [Step 3/4] Checking and downloading models in models/ ---"
STEM=""
for n in stemsplit_drums.onnx htdemucs_ft_drums.onnx stemsplitio.onnx; do
    if [ -f "models/$n" ]; then
        STEM="models/$n"
        break
    fi
done

if [ -z "$STEM" ]; then
    echo "Downloading stemsplit_drums.onnx [HT-Demucs FT drums]..."
    URL="https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx"
    PART="models/stemsplit_drums.onnx.part"
    if command -v curl >/dev/null 2>&1; then
        curl -L --fail --retry 5 --retry-delay 3 -C - --progress-bar -o "$PART" "$URL" || true
    elif command -v wget >/dev/null 2>&1; then
        wget -c -O "$PART" "$URL" || true
    else
        echo "[WARNING] neither curl nor wget — skip stemsplit download"
    fi
    if [ -f "$PART" ] && [ "$(wc -c < "$PART" | tr -d ' ')" -gt 1000000 ]; then
        mv -f "$PART" models/stemsplit_drums.onnx
        STEM="models/stemsplit_drums.onnx"
        echo "   [OK] stemsplit_drums.onnx downloaded"
    else
        rm -f "$PART"
        echo "   [WARNING] stemsplit download failed or incomplete."
        echo "   Manual: $URL"
    fi
else
    echo "   [OK] drum separation model: $STEM"
fi

if [ ! -f models/adtof.onnx ]; then
    if command -v python3 >/dev/null 2>&1 && [ -f export_adtof_from_local_pytorch_to_onnx.py ]; then
        echo "[INFO] adtof.onnx missing. Attempting export via python3..."
        python3 export_adtof_from_local_pytorch_to_onnx.py --out models/adtof.onnx >/dev/null 2>&1 || true
    elif command -v python >/dev/null 2>&1 && [ -f export_adtof_from_local_pytorch_to_onnx.py ]; then
        echo "[INFO] adtof.onnx missing. Attempting export via python..."
        python export_adtof_from_local_pytorch_to_onnx.py --out models/adtof.onnx >/dev/null 2>&1 || true
    fi
fi

echo ""
echo "--- [Step 4/4] Compiling pata2fla_gui (GUI) and pata2fla (CLI) ---"
echo "Compiling CLI (pata2fla)..."
export CGO_ENABLED=0
if ! go build -ldflags="-s -w" -o pata2fla ./cmd/pata2fla_cli; then
    echo "[ERROR] CLI compilation failed!"
    exit 1
fi

echo "Compiling GUI (pata2fla_gui)..."
export CGO_ENABLED=1
if ! go build -ldflags="-s -w" -o pata2fla_gui ./cmd/pata2fla_gui; then
    echo "[ERROR] GUI compilation failed!"
    exit 1
fi
chmod 755 pata2fla pata2fla_gui run.sh run_ui.sh
echo "   [OK] pata2fla_gui (GUI) and pata2fla (CLI) compiled"

echo ""
echo "==================================================================="
echo "  Runtime Status:"
echo "==================================================================="
if [ -f libonnxruntime.so ] || [ -f libonnxruntime.dylib ]; then
    echo "   [+] onnxruntime library    [READY]"
else
    echo "   [-] onnxruntime library    [MISSING]"
fi

echo ""
echo "==================================================================="
echo "  Models Status:"
echo "==================================================================="
for n in stemsplit_drums.onnx htdemucs_ft_drums.onnx stemsplitio.onnx adtof.onnx hh_best.onnx cy_best.onnx tt_best.onnx cy_arbiter.onnx; do
    if [ -f "models/$n" ]; then
        echo "   [+] models/$n"
    else
        case "$n" in
            stemsplit*|htdemucs*|stemsplitio*) echo "   [-] models/$n  (needed for full-mix separation)" ;;
            adtof.onnx) echo "   [-] models/$n  (optional / acoustic fallback)" ;;
            *) echo "   [?] models/$n  (optional specialist)" ;;
        esac
    fi
done

echo
echo "==================================================================="
echo "  Header Assets Status (assets/):"
echo "==================================================================="

if [ -f "assets/background.png" ]; then
    echo "   [+] assets/background.png     [CUSTOM BACKGROUND READY]"
else
    echo "   [-] assets/background.png     [OPTIONAL: place background here]"
fi

if [ -f "assets/IconPata2Fla.png" ]; then
    echo "   [+] assets/IconPata2Fla.png   [ICON READY]"
else
    echo "   [-] assets/IconPata2Fla.png   [OPTIONAL: place icon here]"
fi

if [ -f "assets/logo.png" ]; then
    echo "   [+] assets/logo.png           [LOGO READY]"
else
    echo "   [-] assets/logo.png           [OPTIONAL: place logo here]"
fi

if [ -f "assets/logo1.png" ]; then
    echo "   [+] assets/logo1.png          [LOGO1 READY]"
else
    echo "   [-] assets/logo1.png          [OPTIONAL: place logo1 here]"
fi

echo ""
echo "==================================================================="
echo "  SETUP COMPLETED SUCCESSFULLY!"
echo "==================================================================="
echo "Launch GUI:"
echo "   ./pata2fla_gui"
echo "   ./run_ui.sh"
echo "Launch CLI:"
echo "   ./pata2fla -i input/track.flac -o output"
echo "   ./pata2fla --drums-only -i stem.wav -o output"
echo "==================================================================="
