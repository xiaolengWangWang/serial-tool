# 从已校验的 Windows ZIP 生成完整 7z 包，以及只含主程序的 ZIP / 7z 包。
# EXE 内容保持不变；需要 Windows 自带的 bsdtar（支持 7zip / LZMA2）。
# 示例：powershell -File scripts/compress-windows.ps1 -SourceArchive build/CommBox-0.9.4-Windows-x64.zip
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SourceArchive,
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
$source = (Resolve-Path -LiteralPath $SourceArchive).Path
if ([IO.Path]::GetExtension($source) -ne '.zip') { throw 'SourceArchive must be a ZIP file' }
if (!$OutputDirectory) { $OutputDirectory = Split-Path -Parent $source }
$output = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $output | Out-Null
$tar = (Get-Command tar.exe -ErrorAction Stop).Source
$baseName = [IO.Path]::GetFileNameWithoutExtension($source)
$full7z = Join-Path $output ($baseName + '.7z')
$guiZip = Join-Path $output ($baseName + '-GUI.zip')
$gui7z = Join-Path $output ($baseName + '-GUI.7z')
$guiNames = @('CommBox.exe', 'README-Windows.txt')
$expectedNames = @('CommBox.exe', 'CommBox-CLI.exe', 'VirtualCOM-GUI.exe', 'VirtualCOM.exe', 'README-Windows.txt', 'VirtualCOM使用说明.md', 'SHA256SUMS.txt')

function Get-StreamHash {
    param([IO.Stream]$Stream)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { [BitConverter]::ToString($sha.ComputeHash($Stream)).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}

function Test-SevenZip {
    param([string]$Path, [hashtable]$ExpectedHashes)
    $listing = New-Object Diagnostics.ProcessStartInfo
    $listing.FileName = $tar
    $listing.Arguments = '-tf "' + $Path + '"'
    $listing.UseShellExecute = $false
    $listing.CreateNoWindow = $true
    $listing.RedirectStandardOutput = $true
    # Windows bsdtar 的重定向列表使用系统 ANSI 编码，与控制台编码无关。
    $listing.StandardOutputEncoding = [Text.Encoding]::GetEncoding([Globalization.CultureInfo]::CurrentCulture.TextInfo.ANSICodePage)
    $process = [Diagnostics.Process]::Start($listing)
    try {
        $names = @($process.StandardOutput.ReadToEnd() -split '\r?\n' | Where-Object { $_ })
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw ('Cannot list archive: ' + $Path) }
    } finally { $process.Dispose() }
    if ($names.Count -ne $ExpectedHashes.Count) { throw ('Archive entry count mismatch: ' + $Path) }
    foreach ($name in $names) {
        if (!$ExpectedHashes.ContainsKey($name)) { throw ('Unexpected archive entry: ' + $name) }
        # 二进制 stdout 直接进入 SHA256，不经过 PowerShell 文本管道或落盘解压。
        $psi = New-Object Diagnostics.ProcessStartInfo
        $psi.FileName = $tar
        $psi.Arguments = '-xOf "' + $Path + '" "' + $name + '"'
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        $psi.RedirectStandardOutput = $true
        $process = [Diagnostics.Process]::Start($psi)
        try {
            $hash = Get-StreamHash $process.StandardOutput.BaseStream
            $process.WaitForExit()
            if ($process.ExitCode -ne 0 -or $hash -ne $ExpectedHashes[$name]) {
                throw ('Extracted SHA256 mismatch: ' + $name)
            }
        } finally { $process.Dispose() }
    }
}

$archive = [IO.Compression.ZipFile]::OpenRead($source)
$fullHashes = @{}
$guiHashes = @{}
try {
    if ($archive.Entries.Count -ne $expectedNames.Count) { throw 'Unexpected source ZIP entry count' }
    foreach ($entry in $archive.Entries) {
        if ($expectedNames -notcontains $entry.FullName -or $fullHashes.ContainsKey($entry.FullName)) {
            throw ('Unexpected or duplicate source entry: ' + $entry.FullName)
        }
        $stream = $entry.Open()
        try { $fullHashes[$entry.FullName] = Get-StreamHash $stream }
        finally { $stream.Dispose() }
        if ($entry.FullName -match '\.(md|txt)$') {
            $reader = New-Object IO.StreamReader($entry.Open(), [Text.Encoding]::UTF8)
            try { $text = $reader.ReadToEnd() } finally { $reader.Dispose() }
            if ($text.Contains('E-SafeNet')) { throw ('Encrypted document in source ZIP: ' + $entry.FullName) }
        }
    }
    $reader = New-Object IO.StreamReader($archive.GetEntry('SHA256SUMS.txt').Open(), [Text.Encoding]::UTF8)
    try { $manifest = $reader.ReadToEnd() } finally { $reader.Dispose() }
    $declared = @{}
    foreach ($line in ($manifest -split '\r?\n')) {
        if (!$line.Trim()) { continue }
        if ($line -notmatch '^([0-9a-fA-F]{64})  (.+)$') { throw 'Invalid SHA256SUMS line' }
        $name = $matches[2]
        if ($name -eq 'SHA256SUMS.txt' -or $declared.ContainsKey($name) -or !$fullHashes.ContainsKey($name)) {
            throw ('Unexpected checksum entry: ' + $name)
        }
        $declared[$name] = $matches[1].ToLowerInvariant()
        if ($declared[$name] -ne $fullHashes[$name]) { throw ('Source ZIP checksum mismatch: ' + $name) }
    }
    if ($declared.Count -ne $expectedNames.Count - 1) { throw 'Incomplete source SHA256SUMS' }

    # 只从 ZIP 的内存流复制，避免本机 DLP 将临时 .md/.txt 落盘为密文。
    $targetFile = [IO.File]::Open($guiZip, [IO.FileMode]::Create)
    $target = New-Object IO.Compression.ZipArchive($targetFile, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in $guiNames) {
            $original = $archive.GetEntry($name)
            $entry = $target.CreateEntry($name, [IO.Compression.CompressionLevel]::Optimal)
            $entry.LastWriteTime = $original.LastWriteTime
            $inputStream, $outputStream = $original.Open(), $entry.Open()
            try { $inputStream.CopyTo($outputStream) } finally { $inputStream.Dispose(); $outputStream.Dispose() }
            $guiHashes[$name] = $fullHashes[$name]
        }
        $sums = (($guiNames | ForEach-Object { $guiHashes[$_] + '  ' + $_ }) -join "`n") + "`n"
        $bytes = [Text.Encoding]::UTF8.GetBytes($sums)
        $entry = $target.CreateEntry('SHA256SUMS.txt', [IO.Compression.CompressionLevel]::Optimal)
        $stream = $entry.Open()
        try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
        $memory = New-Object IO.MemoryStream(, $bytes)
        try { $guiHashes['SHA256SUMS.txt'] = Get-StreamHash $memory } finally { $memory.Dispose() }
    } finally { $target.Dispose(); $targetFile.Dispose() }
} finally { $archive.Dispose() }

# @archive 将 ZIP 内的文件直接转封装为 7z，无需解压到磁盘。
& $tar -c --format 7zip --options '7zip:compression=lzma2,7zip:compression-level=9' -f $full7z ('@' + $source)
if ($LASTEXITCODE -ne 0) { throw 'Full 7z compression failed' }
Test-SevenZip -Path $full7z -ExpectedHashes $fullHashes
& $tar -c --format 7zip --options '7zip:compression=lzma2,7zip:compression-level=9' -f $gui7z ('@' + $guiZip)
if ($LASTEXITCODE -ne 0) { throw 'GUI 7z compression failed' }
Test-SevenZip -Path $gui7z -ExpectedHashes $guiHashes

$packedGui = [IO.Compression.ZipFile]::OpenRead($guiZip)
try {
    if ($packedGui.Entries.Count -ne $guiHashes.Count) { throw 'GUI ZIP entry count mismatch' }
    foreach ($entry in $packedGui.Entries) {
        $stream = $entry.Open()
        try { $hash = Get-StreamHash $stream } finally { $stream.Dispose() }
        if ($hash -ne $guiHashes[$entry.FullName]) { throw ('GUI ZIP checksum mismatch: ' + $entry.FullName) }
    }
} finally { $packedGui.Dispose() }

foreach ($path in @($source, $full7z, $guiZip, $gui7z)) {
    [pscustomobject]@{
        Path = $path
        Bytes = (Get-Item -LiteralPath $path).Length
        MiB = [math]::Round((Get-Item -LiteralPath $path).Length / 1MB, 2)
        SHA256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}
