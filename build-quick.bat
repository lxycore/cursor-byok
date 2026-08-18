@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo [1/2] Building frontend...
cd frontend
call yarn build
if %errorlevel% neq 0 (
    echo FAILED: Frontend build error
    pause
    exit /b %errorlevel%
)
cd ..

echo [2/2] Building Go binary...
go build -ldflags "-H windowsgui" -o bin/CursorUltra_test.exe .
if %errorlevel% neq 0 (
    echo FAILED: Go build error
    pause
    exit /b %errorlevel%
)

echo.
echo DONE: bin\CursorUltra_test.exe
pause
