#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

if [ ! -x "./pata2fla_ui" ]; then
    echo "[INFO] pata2fla_ui not found — running setup_ui.sh first..."
    bash ./setup_ui.sh || exit 1
fi

exec ./pata2fla_ui "$@"
