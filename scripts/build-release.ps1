# 构建 Windows x64 发布包。版本号从源码常量读出,产物写到 build/。
#
# 用法:
#   powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1 -SkipTagCheck   # 本地验证,不出正式包
[CmdletBinding()]
param([switch]$SkipTagCheck)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot

function Get-GoConst {
    param([string]$Path, [string]$Name)
    $text = [IO.File]::ReadAllText((Join-Path $repoRoot $Path))
    $m = [regex]::Match($text, ('(?m)^const\s+{0}\s*=\s*"([^"]+)"' -f $Name))
    if (-not $m.Success) { throw ('Cannot read ' + $Name + ' from ' + $Path) }
    return $m.Groups[1].Value
}

# 文档从 git 取,不从工作区复制:本机 DLP 把 .md/.txt 在磁盘上存成密文,
# 直接复制会把密文打进发布包。git 读到的是明文。
function Export-GitText {
    param([string]$Revision, [string]$Path, [string]$Destination)
    $psi = New-Object Diagnostics.ProcessStartInfo
    $psi.FileName = 'git'
    $psi.Arguments = 'show ' + $Revision + ':' + $Path
    $psi.WorkingDirectory = $repoRoot
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $process = [Diagnostics.Process]::Start($psi)
    $buffer = New-Object IO.MemoryStream
    $process.StandardOutput.BaseStream.CopyTo($buffer)
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) { throw ('git show failed for ' + $Path) }
    $bytes = $buffer.ToArray()
    if ($bytes.Length -eq 0) { throw ('Empty document: ' + $Path) }
    $text = [Text.Encoding]::UTF8.GetString($bytes)
    if ($text.Contains('E-SafeNet')) { throw ('Document is still encrypted: ' + $Path) }
    $text = $text.Replace("`r`n", "`n").Replace("`n", "`r`n")
    [IO.File]::WriteAllText($Destination, $text, (New-Object Text.UTF8Encoding($true)))
}

$version = Get-GoConst 'internal\wincore\modes.go' 'Version'
$vcomVersion = Get-GoConst 'virtualcom\internal\vcom\version.go' 'Version'
$outputDir = Join-Path $repoRoot ('build\windows-' + $version)
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

Push-Location $repoRoot
try {
    if ((git status --porcelain).Length -ne 0) { throw 'Release source tree must be clean' }
    $tag = 'v' + $version
    if ($SkipTagCheck) {
        Write-Warning ('Skipping the ' + $tag + ' tag check; this build is NOT a release build')
    } else {
        git rev-parse --verify --quiet ($tag + '^{}') > $null
        if ($LASTEXITCODE -ne 0) { throw ('Tag ' + $tag + ' does not exist; create it or pass -SkipTagCheck for a local build') }
        if ((git rev-parse HEAD) -ne (git rev-parse ($tag + '^{}'))) { throw ('HEAD must match release tag ' + $tag) }
    }
    $commit = (git rev-parse HEAD).Trim()

    # -w 去掉 DWARF 调试信息,体积约减 25%,panic 栈仍带函数名。
    # 不要加 -s:剥符号的 GUI 程序在装了 360 的机器上会被当成加壳投放器隔离。
    go build -trimpath -ldflags '-H windowsgui -w' -o (Join-Path $outputDir 'CommBox.exe') ./windows
    if ($LASTEXITCODE -ne 0) { throw 'CommBox GUI build failed' }
    go build -trimpath -ldflags '-w' -o (Join-Path $outputDir 'CommBox-CLI.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'CommBox CLI build failed' }
    Push-Location 'virtualcom'
    try {
        go build -trimpath -ldflags '-H windowsgui -w' -o (Join-Path $outputDir 'VirtualCOM-GUI.exe') ./cmd/virtualcom-gui
        if ($LASTEXITCODE -ne 0) { throw 'VirtualCOM GUI build failed' }
        go build -trimpath -ldflags '-w' -o (Join-Path $outputDir 'VirtualCOM.exe') ./cmd/virtualcom
        if ($LASTEXITCODE -ne 0) { throw 'VirtualCOM CLI build failed' }
    } finally { Pop-Location }

    Export-GitText -Revision 'HEAD' -Path 'docs/virtualcom-commbox-compat.md' -Destination (Join-Path $outputDir 'VirtualCOM使用说明.md')
    Export-GitText -Revision 'HEAD' -Path 'windows/README-Windows.txt' -Destination (Join-Path $outputDir 'README-Windows.txt')

    # subsystem: 2 = Windows GUI, 3 = 控制台
    $binaries = @{
        'CommBox.exe'        = @{ Subsystem = 2; Version = $version }
        'CommBox-CLI.exe'    = @{ Subsystem = 3; Version = $version }
        'VirtualCOM-GUI.exe' = @{ Subsystem = 2; Version = $vcomVersion }
        'VirtualCOM.exe'     = @{ Subsystem = 3; Version = $vcomVersion }
    }
    foreach ($name in $binaries.Keys) {
        $path = Join-Path $outputDir $name
        $bytes = [IO.File]::ReadAllBytes($path)
        $pe = [BitConverter]::ToInt32($bytes, 0x3c)
        if ([BitConverter]::ToUInt32($bytes, $pe) -ne 0x4550 -or [BitConverter]::ToUInt16($bytes, $pe + 4) -ne 0x8664 -or [BitConverter]::ToUInt16($bytes, $pe + 24 + 68) -ne $binaries[$name].Subsystem) {
            throw ('Invalid Windows x64 executable: ' + $name)
        }
        $metadata = (go version -m $path) -join "`n"
        if ($LASTEXITCODE -ne 0 -or !$metadata.Contains('vcs.revision=' + $commit) -or !$metadata.Contains('vcs.modified=false')) {
            throw ('Binary does not match clean source: ' + $name)
        }
        $fileVersion = (Get-Item $path).VersionInfo.FileVersion
        if ($fileVersion -ne $binaries[$name].Version) {
            throw ('Wrong version resource in ' + $name + ': ' + $fileVersion + ' != ' + $binaries[$name].Version)
        }
    }
    $cliVersion = & (Join-Path $outputDir 'CommBox-CLI.exe') -version
    if ($LASTEXITCODE -ne 0 -or $cliVersion.Trim() -ne $version) { throw 'Wrong CommBox CLI version' }
    $virtualVersion = & (Join-Path $outputDir 'VirtualCOM.exe') version
    if ($LASTEXITCODE -ne 0 -or !$virtualVersion.StartsWith('VirtualCOM ' + $vcomVersion + ' ')) { throw 'Wrong VirtualCOM version' }

    $files = @('CommBox.exe', 'CommBox-CLI.exe', 'VirtualCOM-GUI.exe', 'VirtualCOM.exe', 'README-Windows.txt', 'VirtualCOM使用说明.md')
    $sums = foreach ($name in $files) { '{0}  {1}' -f (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $outputDir $name)).Hash.ToLowerInvariant(), $name }
    [IO.File]::WriteAllText((Join-Path $outputDir 'SHA256SUMS.txt'), ($sums -join "`n") + "`n", (New-Object Text.UTF8Encoding($false)))
    $files += 'SHA256SUMS.txt'

    $archivePath = Join-Path $repoRoot ('build\CommBox-' + $version + '-Windows-x64.zip')
    Compress-Archive -LiteralPath @($files | ForEach-Object { Join-Path $outputDir $_ }) -DestinationPath $archivePath -Force
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        if ($archive.Entries.Count -ne $files.Count) { throw 'ZIP entry count mismatch' }
        foreach ($entry in $archive.Entries) {
            if ($files -notcontains $entry.FullName) { throw ('Unexpected ZIP entry: ' + $entry.FullName) }
            $stream = $entry.Open()
            $sha = [Security.Cryptography.SHA256]::Create()
            try { $hash = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() } finally { $stream.Dispose(); $sha.Dispose() }
            if ($hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $outputDir $entry.FullName)).Hash.ToLowerInvariant()) {
                throw ('ZIP contents mismatch: ' + $entry.FullName)
            }
        }
    } finally { $archive.Dispose() }

    Write-Output ('CommBox ' + $version + ' / VirtualCOM ' + $vcomVersion + ' built from clean commit ' + $commit)
    Get-ChildItem (Join-Path $outputDir '*.exe') | ForEach-Object { '{0,-20} {1,7:N2} MB' -f $_.Name, ($_.Length / 1MB) }
    Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath | Format-List
} finally { Pop-Location }
