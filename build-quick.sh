#!/bin/bash
set -e
cd "$(dirname "$0")"

echo "[1/2] Building frontend..."
cd frontend
yarn build
cd ..

echo "[2/2] Building Go binary..."
go build -ldflags "-H windowsgui" -o bin/CursorUltra_test.exe .

echo ""
echo "[DONE] Output: bin/CursorUltra_test.exe"

# If --run flag passed, launch the built binary in background
if [ "$1" = "--run" ]; then
    echo "Launching CursorUltra_test.exe ..."
    ./bin/CursorUltra_test.exe &
    echo "PID: $!"
fi
