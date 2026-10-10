# 把 GitHub 上已发布的版本同步到 Gitee 镜像：推送这个版本的提交到 main 并推 tag，
# 建同名发行版，上传与 GitHub 相同的说明和附件。软件内更新优先从 Gitee 查询和下载，
# 所以每次 GitHub 发布成功后都要运行一次。只同步指定版本，不搬旧 tag 和旧发行版。
#
# 令牌：Gitee 私人令牌（勾选 projects），放在 ~/.gitee_token 或环境变量 GITEE_TOKEN，
# 只放在请求头里，不进命令行、网址和日志。
#
# 附件全程在内存里：从 GitHub 下载、按发布说明的 SHA256 校验、直接上传，不落盘，
# 避免本机 DLP 把 .md/.txt 存成密文后传上去。
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File scripts/publish-gitee.ps1             # 版本号取源码常量
#   powershell -ExecutionPolicy Bypass -File scripts/publish-gitee.ps1 -Tag v0.11.3
[CmdletBinding()]
param([string]$Tag)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
# 与 core/update.go 的 updateGitHubRepo / updateGiteeRepo 一致
$githubRepo = 'xiaolengWangWang/serial-tool'
$giteeRepo = 'sun_xuanqi/serial-tool'
$giteeApi = 'https://gitee.com/api/v5/repos/' + $giteeRepo

if (!$Tag) {
    $text = [IO.File]::ReadAllText((Join-Path $repoRoot 'core\modes.go'))
    $m = [regex]::Match($text, '(?m)^const\s+Version\s*=\s*"([^"]+)"')
    if (!$m.Success) { throw 'Cannot read Version from core/modes.go' }
    $Tag = 'v' + $m.Groups[1].Value
}
$token = $env:GITEE_TOKEN
if (!$token) {
    $tokenPath = Join-Path $HOME '.gitee_token'
    if (Test-Path -LiteralPath $tokenPath) { $token = ([IO.File]::ReadAllText($tokenPath)).Trim() }
}
if (!$token) { throw 'No Gitee token: put a private token (projects scope) in ~/.gitee_token or set GITEE_TOKEN' }

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Add-Type -AssemblyName System.Net.Http

function New-Client {
    param([string]$Bearer)
    $client = New-Object Net.Http.HttpClient
    # 国内连 GitHub 握手慢，上传十几 MB 也要时间
    $client.Timeout = [TimeSpan]::FromMinutes(15)
    $client.DefaultRequestHeaders.UserAgent.ParseAdd('CommBox-Release')
    if ($Bearer) { $client.DefaultRequestHeaders.Authorization = New-Object Net.Http.Headers.AuthenticationHeaderValue('Bearer', $Bearer) }
    return $client
}

function Send-Request {
    param([Net.Http.HttpClient]$Client, [string]$Method, [string]$Url, [Net.Http.HttpContent]$Content, [switch]$AllowNotFound)
    for ($attempt = 1; ; $attempt++) {
        $request = New-Object Net.Http.HttpRequestMessage((New-Object Net.Http.HttpMethod($Method)), $Url)
        if ($Content) { $request.Content = $Content }
        try {
            $response = $Client.SendAsync($request).GetAwaiter().GetResult()
        } catch {
            # 只重试网络错误；带内容的请求重发前内容流已读过，不重试
            if ($attempt -ge 3 -or $Content) { throw }
            Start-Sleep -Seconds 3
            continue
        }
        $status = [int]$response.StatusCode
        if ($AllowNotFound -and $status -eq 404) { return $null }
        if ($status -ge 500 -and $attempt -lt 3 -and !$Content) { Start-Sleep -Seconds 3; continue }
        if (!$response.IsSuccessStatusCode) {
            $text = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            throw ($Method + ' ' + ([Uri]$Url).AbsolutePath + ' -> ' + $status + ': ' + $text)
        }
        return $response
    }
}

function Read-Json {
    param($Response)
    if ($null -eq $Response) { return $null }
    $text = $Response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    if (!$text.Trim()) { return $null }
    return $text | ConvertFrom-Json
}

function Invoke-Gitee {
    param([string]$Method, [string]$Path, $Body, [switch]$AllowNotFound)
    $content = $null
    if ($null -ne $Body) {
        $json = $Body | ConvertTo-Json -Depth 5 -Compress
        $content = New-Object Net.Http.StringContent($json, [Text.Encoding]::UTF8, 'application/json')
    }
    return Read-Json (Send-Request $gitee $Method ($giteeApi + $Path) $content -AllowNotFound:$AllowNotFound)
}

function Get-Sha256 {
    param([byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($sha.ComputeHash($Bytes)).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}

$github = New-Client
$gitee = New-Client $token
$anonymous = New-Client
try {
    # 1. GitHub 发布：正式版，说明里每个附件都有 SHA256（SHA256SUMS.txt 自己除外）
    $release = Read-Json (Send-Request $github 'GET' ('https://api.github.com/repos/' + $githubRepo + '/releases/tags/' + $Tag))
    if ($release.draft -or $release.prerelease) { throw ('GitHub release ' + $Tag + ' is a draft or prerelease') }
    $sums = @{}
    foreach ($m in [regex]::Matches($release.body, '(?m)^([0-9a-f]{64})  (\S+)\s*$')) { $sums[$m.Groups[2].Value] = $m.Groups[1].Value }
    $assets = @($release.assets)
    if ($assets.Count -eq 0) { throw ('GitHub release ' + $Tag + ' has no assets') }
    foreach ($a in $assets) {
        if ($a.name -ne 'SHA256SUMS.txt' -and !$sums.ContainsKey($a.name)) { throw ('No SHA256 for ' + $a.name + ' in release notes') }
    }

    # 2. 代码：这个版本的提交推到 Gitee main，再推 tag。认证头经环境变量交给 git
    $commit = (git -C $repoRoot rev-parse --verify --quiet ($Tag + '^{commit}'))
    if ($LASTEXITCODE -ne 0 -or !$commit) { throw ('Tag ' + $Tag + ' does not exist locally; run git fetch origin --tags') }
    if ($commit.Trim() -ne $release.target_commitish -and $release.target_commitish -match '^[0-9a-f]{40}$') {
        throw ('Local tag ' + $Tag + ' does not match the GitHub release commit')
    }
    $owner = $giteeRepo.Split('/')[0]
    $basic = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes($owner + ':' + $token))
    $env:GIT_CONFIG_COUNT = '2'
    $env:GIT_CONFIG_KEY_0 = 'http.https://gitee.com/.extraHeader'
    $env:GIT_CONFIG_VALUE_0 = 'Authorization: Basic ' + $basic
    $env:GIT_CONFIG_KEY_1 = 'credential.helper'
    $env:GIT_CONFIG_VALUE_1 = ''
    try {
        git -C $repoRoot push ('https://gitee.com/' + $giteeRepo + '.git') ($commit.Trim() + ':refs/heads/main') ('refs/tags/' + $Tag + ':refs/tags/' + $Tag)
        if ($LASTEXITCODE -ne 0) { throw 'git push to Gitee failed' }
    } finally {
        foreach ($name in 'GIT_CONFIG_COUNT', 'GIT_CONFIG_KEY_0', 'GIT_CONFIG_VALUE_0', 'GIT_CONFIG_KEY_1', 'GIT_CONFIG_VALUE_1') {
            Remove-Item ('Env:' + $name) -ErrorAction SilentlyContinue
        }
    }

    # 3. Gitee 发行版：没有就建（Gitee 没有草稿，建了就公开），说明不同就改
    $body = @{ tag_name = $Tag; name = $release.name; body = $release.body; prerelease = $false }
    $target = Invoke-Gitee 'GET' ('/releases/tags/' + $Tag) -AllowNotFound
    if ($null -eq $target -or !$target.id) {
        $body.target_commitish = $commit.Trim()
        $target = Invoke-Gitee 'POST' '/releases' $body
        Write-Output ('Created Gitee release ' + $Tag)
    } elseif ($target.body -ne $release.body -or $target.name -ne $release.name) {
        $target = Invoke-Gitee 'PATCH' ('/releases/' + $target.id) $body
        Write-Output ('Updated Gitee release notes for ' + $Tag)
    }

    # 4. 附件：同名同大小的跳过，大小不对的删掉重传
    $files = @(Invoke-Gitee 'GET' ('/releases/' + $target.id + '/attach_files?per_page=100'))
    foreach ($a in $assets) {
        $have = @($files | Where-Object { $_.name -eq $a.name })
        if ($have.Count -eq 1 -and [int64]$have[0].size -eq [int64]$a.size) { Write-Output ('Already on Gitee: ' + $a.name); continue }
        foreach ($f in $have) { $null = Invoke-Gitee 'DELETE' ('/releases/' + $target.id + '/attach_files/' + $f.id) }

        $bytes = (Send-Request $github 'GET' $a.browser_download_url).Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        if ($bytes.Length -ne [int64]$a.size) { throw ('Downloaded size mismatch: ' + $a.name) }
        if ($a.name -ne 'SHA256SUMS.txt' -and (Get-Sha256 $bytes) -ne $sums[$a.name]) { throw ('SHA256 mismatch: ' + $a.name) }

        $form = New-Object Net.Http.MultipartFormDataContent
        $file = New-Object Net.Http.ByteArrayContent(, $bytes)
        $file.Headers.ContentType = New-Object Net.Http.Headers.MediaTypeHeaderValue('application/octet-stream')
        $form.Add($file, 'file', $a.name)
        $null = Send-Request $gitee 'POST' ($giteeApi + '/releases/' + $target.id + '/attach_files') $form
        Write-Output ('Uploaded to Gitee: {0} ({1:N2} MiB)' -f $a.name, ($bytes.Length / 1MB))
    }

    # 5. 按软件内更新的方式匿名核对：latest 是这个版本，附件齐全且大小对，Windows 安装包能下载且校验一致
    $latest = Read-Json (Send-Request $anonymous 'GET' ($giteeApi + '/releases/latest'))
    if ($latest.tag_name -ne $Tag) { throw ('Gitee latest release is ' + $latest.tag_name + ', expected ' + $Tag) }
    $files = @(Invoke-Gitee 'GET' ('/releases/' + $target.id + '/attach_files?per_page=100'))
    foreach ($a in $assets) {
        $have = @($files | Where-Object { $_.name -eq $a.name -and [int64]$_.size -eq [int64]$a.size })
        if ($have.Count -ne 1) { throw ('Gitee attachment missing, duplicated or wrong size: ' + $a.name) }
    }
    $zip = $latest.assets | Where-Object { $_.name -like '*-Windows-x64.zip' } | Select-Object -First 1
    if (!$zip) { throw 'Gitee latest release has no Windows x64 zip' }
    $bytes = (Send-Request $anonymous 'GET' $zip.browser_download_url).Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
    if ((Get-Sha256 $bytes) -ne $sums[$zip.name]) { throw ('Gitee download SHA256 mismatch: ' + $zip.name) }
    Write-Output ('Gitee release ' + $Tag + ' verified: ' + $assets.Count + ' attachments, ' + $zip.name + ' downloads with matching SHA256')
    Write-Output ('https://gitee.com/' + $giteeRepo + '/releases/tag/' + $Tag)
} finally {
    $github.Dispose()
    $gitee.Dispose()
    $anonymous.Dispose()
}
