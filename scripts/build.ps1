$ErrorActionPreference = "Stop"
$goos = (go env GOOS).Trim()
$goarch = (go env GOARCH).Trim()
$ext = if ($goos -eq "windows") { "dll" } elseif ($goos -eq "darwin") { "dylib" } else { "so" }
$out = Join-Path "dist" (Join-Path $goos $goarch)
New-Item -ItemType Directory -Force -Path $out | Out-Null
$env:CGO_ENABLED = "1"
go build -trimpath -buildmode=c-shared -o (Join-Path $out "qoder.$ext") .
Remove-Item -ErrorAction SilentlyContinue (Join-Path $out "qoder.h")
Write-Output (Join-Path $out "qoder.$ext")
