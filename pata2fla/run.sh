#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR" || exit 1

if [ ! -x "./pata2fla" ]; then
    echo "[INFO] pata2fla not found - running setup_ui.sh first..."
    bash ./setup_ui.sh || exit 1
    [ -x "./pata2fla" ] || { echo "[ERROR] build failed"; exit 1; }
fi

echo "[run.sh] launching CLI: ./pata2fla"
echo "[run.sh] args: $*"
exec ./pata2fla --cli "$@"
