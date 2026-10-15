param([string]$Output = 'bin')
$ErrorActionPreference = 'Stop'
$version = '26.2.4'
$archive = Join-Path $env:TEMP "1loop-mesa-$version.7z"
Invoke-WebRequest -UseBasicParsing "https://github.com/pal1000/mesa-dist-win/releases/download/$version/mesa3d-$version-release-msvc.7z" -OutFile $archive
if ((Get-FileHash $archive -Algorithm SHA256).Hash -ne '351FC8C8B695878FFB3EAA044B3EAD08672A48B1A045E3C3E3975811DF0F6695') {
    throw 'Mesa archive checksum mismatch'
}
$software = Join-Path $Output 'software'
New-Item -ItemType Directory -Force $software | Out-Null
& 7z e $archive 'x64/opengl32.dll' 'x64/libgallium_wgl.dll' "-o$software" -y
if ($LASTEXITCODE -ne 0) { throw 'Mesa extraction failed' }
Copy-Item (Join-Path $Output '1loop.exe') $software
@'
@echo off
setlocal
rem Use the portable CPU renderer on systems without a working OpenGL driver.
set GALLIUM_DRIVER=llvmpipe
set LP_NUM_THREADS=1
start "" "%~dp0software\1loop.exe" %*
'@ | Set-Content (Join-Path $Output '1loop-software.cmd') -Encoding ascii
$licenses = Join-Path $software 'licenses'
New-Item -ItemType Directory -Force $licenses | Out-Null
Copy-Item (Join-Path $PSScriptRoot 'windows-licenses/*') $licenses
