#!/usr/bin/env bash
set -u
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

echo "==================================================================="
echo "   PATA2FLA (STANDALONE GO EDITION) - SETUP & BUILD"
echo "==================================================================="

if ! command -v go >/dev/null 2>&1; then
    echo "[ERROR] Go compiler is not installed!"
    echo "Install Go: https://go.dev/dl/  (or: brew install go / sudo apt install golang)"
    exit 1
fi
mkdir -p models output input

echo ""
echo "[1/5] Go version:"
go version

echo ""
echo "[2/5] Go modules and onnxruntime library..."
export CGO_ENABLED=0
go mod tidy || { echo "[ERROR] go mod tidy failed (no internet?)"; exit 1; }
VER=1.24.1
OS="$(uname -s)"; ARCH="$(uname -m)"
if [ ! -f libonnxruntime.so ] && [ ! -f libonnxruntime.dylib ]; then
    case "$OS-$ARCH" in
        Linux-x86_64)  PKG="onnxruntime-linux-x64-$VER";    LIBF="libonnxruntime.so.$VER";    OUT="libonnxruntime.so";;
        Linux-aarch64) PKG="onnxruntime-linux-aarch64-$VER"; LIBF="libonnxruntime.so.$VER";   OUT="libonnxruntime.so";;
        Darwin-arm64)  PKG="onnxruntime-osx-arm64-$VER";    LIBF="libonnxruntime.$VER.dylib"; OUT="libonnxruntime.dylib";;
        Darwin-x86_64) PKG="onnxruntime-osx-x86_64-$VER";   LIBF="libonnxruntime.$VER.dylib"; OUT="libonnxruntime.dylib";;
        *) PKG="";;
    esac
    if [ -n "$PKG" ]; then
        echo ">> Downloading onnxruntime $VER ($PKG)..."
        if curl -L --fail -o /tmp/ort.tgz "https://github.com/microsoft/onnxruntime/releases/download/v$VER/$PKG.tgz" \
           && tar -xzf /tmp/ort.tgz -C /tmp; then
            cp "/tmp/$PKG/lib/$LIBF" "./$OUT"
        fi
    fi
fi
if [ ! -f libonnxruntime.so ] && [ ! -f libonnxruntime.dylib ]; then
    echo "[ERROR] onnxruntime $VER shared library missing. Download it from https://github.com/microsoft/onnxruntime/releases/tag/v$VER"
    echo "        and copy the library next to this script as libonnxruntime.so (or .dylib)"
    exit 1
fi
echo "   [OK] onnxruntime library ready"

file_size() { stat -c%s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null || echo 0; }
MIN_BYTES=100000000

echo ""
echo "[3/5] Models in models/ ..."
M1=""
for n in stemsplit_drums.onnx htdemucs_ft_drums.onnx stemsplitio.onnx; do
    [ -f "models/$n" ] && M1="models/$n" && break
done
if [ -n "$M1" ] && [ "$(file_size "$M1")" -lt "$MIN_BYTES" ]; then
    echo "   [!] $M1 is incomplete - removing it"
    rm -f "$M1"; M1=""
fi
if [ -z "$M1" ]; then
    echo ">> Downloading stemsplit_drums.onnx (316 MB, resumable)..."
    URL="https://huggingface.co/StemSplitio/htdemucs-ft-drums-onnx/resolve/main/htdemucs_ft_drums.onnx"
    if command -v curl >/dev/null 2>&1; then
        curl -L --fail --retry 5 --retry-delay 3 -C - --progress-bar -o models/stemsplit_drums.onnx.part "$URL"
    elif command -v wget >/dev/null 2>&1; then
        wget -c -O models/stemsplit_drums.onnx.part "$URL"
    else
        echo "[ERROR] neither curl nor wget found"
    fi
    if [ -f models/stemsplit_drums.onnx.part ] && [ "$(file_size models/stemsplit_drums.onnx.part)" -ge "$MIN_BYTES" ]; then
        mv -f models/stemsplit_drums.onnx.part models/stemsplit_drums.onnx
        echo "   [OK] stemsplit_drums.onnx downloaded"
    else
        echo "   [ERROR] download failed/incomplete - run setup.sh again to resume, or put the file manually at models/stemsplit_drums.onnx"
    fi
else
    echo "   [OK] drum separation model exists: $M1"
fi

if [ ! -f models/adtof.onnx ]; then
    echo ">> adtof.onnx is missing - one-time export from adtof_pytorch (Python is not needed afterwards)..."
    PY=""; for c in python3 python; do command -v $c >/dev/null 2>&1 && PY=$c && break; done
    [ -n "$PY" ] && [ -f export_adtof_from_local_pytorch_to_onnx.py ] && $PY export_adtof_from_local_pytorch_to_onnx.py --out models/adtof.onnx
fi
if [ -f models/adtof.onnx ]; then echo "   [OK] adtof.onnx is ready"
else echo "   [!] adtof.onnx NOT created. Put your own ADTOF ONNX at models/adtof.onnx (or pip install torch onnx git+https://github.com/xavriley/ADTOF-pytorch.git and rerun)"; fi

echo ""
echo "[4/5] Building pata2fla ..."
if ! go build -ldflags="-s -w" -o pata2fla ./cmd/pata2fla_cli; then
    echo "[ERROR] Failed to compile Go project!"
    exit 1
fi
chmod +x pata2fla run.sh 2>/dev/null || true
echo "   [OK] pata2fla built"

echo ""
echo "[5/5] Model status:"
st() { [ -f "models/$1" ] && echo "  [+] $1 [READY]" || echo "  [$2] $1 [$3]"; }
st stemsplit_drums.onnx - MISSING
st adtof.onnx - "MISSING - REQUIRED"
st hh_best.onnx ? "OPTIONAL - put in models/"
st cy_best.onnx ? "OPTIONAL - put in models/"
st tt_best.onnx ? "OPTIONAL - put in models/"
st cy_arbiter.onnx ? "OPTIONAL - put in models/"

echo "==================================================================="
echo "[SUCCESS] Setup complete! Run:"
echo "  ./run.sh --input=\"/path/to/song.flac\" --out=\"/path/to/output\""
echo "==================================================================="
