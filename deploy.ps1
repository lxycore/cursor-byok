# deploy-cursor-byok.ps1 - backup old exe, deploy freshly built exe.
$src = 'D:\python_project\cursor-byok\bin\CursorUltra_test.exe'
$dst = 'D:\cursor-byok\cursor-byok-windows-amd64.exe'
if (-not (Test-Path $src)) { throw "build output not found: $src" }
$bak = "$dst.bak-$(Get-Date -Format 'yyyyMMddHHmmss')"
Copy-Item -LiteralPath $dst -Destination $bak -Force
Write-Output "backup: $bak"
Copy-Item -LiteralPath $src -Destination $dst -Force
$info = Get-Item $dst
Write-Output "deployed: $($info.FullName) ($($info.Length) bytes, $($info.LastWriteTime))"
