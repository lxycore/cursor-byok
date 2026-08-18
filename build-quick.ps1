param(
    [switch]$Run
)

Write-Host "[1/2] Building frontend..." -ForegroundColor Cyan
Set-Location frontend
yarn build
if ($LASTEXITCODE -ne 0) {
    Write-Host "[FAILED] Frontend build failed" -ForegroundColor Red
    exit $LASTEXITCODE
}
Set-Location ..

Write-Host "[2/2] Building Go binary..." -ForegroundColor Cyan
go build -ldflags "-H windowsgui" -o bin/CursorUltra_test.exe .
if ($LASTEXITCODE -ne 0) {
    Write-Host "[FAILED] Go build failed" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host "[DONE] Output: bin/CursorUltra_test.exe" -ForegroundColor Green

if ($Run) {
    Write-Host "Launching CursorUltra_test.exe ..." -ForegroundColor Yellow
    Start-Process -FilePath "bin/CursorUltra_test.exe"
}
