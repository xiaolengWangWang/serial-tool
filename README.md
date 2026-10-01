# CommBox —— 通信调试工具

一个跨平台的串口与网络调试工具:命令行 + macOS / Windows 原生桌面版,共享同一套核心引擎(`internal/wincore`)。支持串口、TCP/UDP 服务端与客户端、串口↔网络透传、HTTP 客户端、虚拟串口,以及全量 SQLite 收发存档。

> 📖 完整使用说明见 **[CommBox 使用手册](docs/CommBox使用手册.md)**。

## 目录
- [命令行](#命令行)
- [桌面版功能总览](#桌面版功能总览)
- [工作模式](#工作模式)
- [HTTP 客户端](#http-客户端)
- [虚拟串口](#虚拟串口macoslinux)
- [数据存储](#数据存储)
- [快捷键(macOS)](#快捷键macos)
- [构建](#构建)

## 命令行

```bash
go build -o commbox .

./commbox -list                                             # 列出串口
./commbox -version                                          # 显示版本号
./commbox -port /dev/tty.usbserial-0001 -baud 115200 -eol crlf   # 文本收发
./commbox -port /dev/tty.usbserial-0001 -baud 9600 -hex -hex-send # HEX 收发
./commbox vserial --host 127.0.0.1 --port 7000              # 虚拟串口(桥接 TCP 到本机串口设备)
```
Windows 串口名可写 `COM3`。`./commbox -h` 查看全部参数。

## 桌面版功能总览

- **收发**：每条数据带毫秒时间戳并区分发送 / 接收；HEX 发送默认开启，HEX / 文本显示可切换；定时发送（最小间隔 10 ms）与循环发送；**历史连接**下拉从数据库读最近 5 个配置，选中自动回填。
- **连接状态**：显示模式、地址、串口参数、运行时间，收发 / 重连 / 错误为本次应用运行累计；被远端断开时按钮与状态自动同步。TCP 客户端首次连接与虚拟串口拨号 10 秒超时。
- **断开日志**写明是哪一端断的：本端主动断开、服务端断开、客户端断开还是链路中断，附断开方式（收到 FIN / 连接被重置 / 读取超时）、对端地址、本次连接时长、本次连接的收发字节与帧数和底层错误；同一条记录写入 SQLite。
- **实时监控**独立窗口：批量刷新并限长、关键字过滤、HEX / ASCII 切换；连接日志每行带时间戳并按错误 / 连接分级高亮，可清空、复制、导出。
- **HTTP 工作区**（macOS ⌘⇧U）：方法 / URL、总超时与连接超时、跟随重定向、跳过 TLS 证书校验，请求头与请求体编辑，cURL 导入与生成（支持 Chrome「复制为 cURL」的 `$'...'` 写法），响应正文 / 响应头分页与 JSON 格式化，响应正文上限 64 MiB。
- **工具箱**：CRC16 Modbus / CCITT-FALSE、CRC32、XOR、SUM、Base64、HEX↔文本、HEX↔十进制（大 / 小端、有符号）、十进制→HEX、Unix 时间戳（自动识别毫秒），结果可复制。
- **本地分析**：Modbus RTU / TCP、IPv4 头、数值候选（UInt16 大小端，UInt32 / Float32 按 ABCD / BADC / CDAB / DCBA）。**数据库分析**可多选数据目录内的 SQLite 文件（⌘ / Shift 多选或全选），按时间、RX / TX 只读查询，最新条数最高 1,000,000（所选文件合计），8 MiB 负载上限、20 条详细解析，省略范围明确显示，不上传 AI。
- **AI 深度分析**（默认关闭，需配置服务与 Key）：
  - macOS：在分析中心内流式对话。点击即发送当前报文、选中报文或数据库报告，回答在结果区逐段显示；可停止并保留部分回答，在下方输入框继续提问，对话自动存入本地 Markdown。
  - Windows：右侧 AI 面板与历史数据分析窗口共用流式对话，可停止、追问、复制与导出；疑似 Modbus 帧附带本地功能码、字节数与 CRC 结论；长报告截取前 32 KiB；服务错误说明 Key、余额、限流或网络原因。AI Key 存 Windows 凭据管理器。
  - Windows 对话可添加文本日志、CSV、JSON、DOCX、可选中文字的 PDF 和 PNG / JPEG / GIF / WebP 图片；附件随提问发送，图片需使用支持视觉的模型（DeepSeek 可选 `deepseek-flash`）。每次最多 6 个附件，文本提取上限 64 KiB / 个，PDF / DOCX 文件上限 5 MiB / 个，PDF 单页解压内容上限 4 MiB，图片上限 2 MiB / 个且不超过 3200 万像素；扫描版 PDF 暂不支持。PDF 在独立进程中解析，限时 20 秒；对话累计文字上限 512 KiB、图片编码上限 24 MiB。
- **在线更新**（帮助 → 检查更新）：启动时自动检查（可关闭），只在新版本带本平台安装包时提示；macOS 按芯片匹配 DMG，下载后校验 SHA256 再打开。

## 工作模式

| 模式 | 说明 |
|---|---|
| **串口** | 串口收发,可配置波特率/数据位/校验/停止位 |
| **TCP** | 用**角色**开关切换服务端/客户端。服务端可选"监听网卡"(`0.0.0.0`=所有网卡/具体 IP/`127.0.0.1`=仅本机);客户端填服务器 IP |
| **UDP** | 同上,UDP 服务端回复最近一次来包的客户端 |
| **串口服务器** | 串口 ↔ 网络双向透明透传;可配置协议(TCP/UDP)、角色、地址 |
| **HTTP 客户端** | 见下 |
| **虚拟串口** | 见下(仅 macOS/Linux) |

**IP 与端口分开填写**;IP 为下拉框,自动列出本机所有网卡地址,也可手输。TCP 服务端发送广播到全部客户端。

## HTTP 客户端

像 curl 一样调 HTTP 接口,自动保持 Cookie 会话(可先登录再调受保护接口)。

**发送框格式**:第一行 `[方法] 路径`(方法省略默认 GET),其后为请求头行,空行后是 body。

```
POST /login
username=admin&password=secret

```
再发:
```
GET /api/v1/health
```

- 方法:GET / POST / PUT / DELETE / PATCH / HEAD / OPTIONS
- body 以 `{` 或 `[` 开头自动设 `Content-Type: application/json`,否则表单;自定义头可覆盖
- 不自动跟随重定向(直接显示 3xx + Set-Cookie,Cookie 仍存入会话)
- 响应显示耗时、字节数,JSON 自动缩进
- 连接期间复用同一 Cookie jar

## 虚拟串口(macOS/Linux)

把一个 TCP 端点桥接成本机虚拟串口设备,供任意串口软件打开(内置等价于 `socat PTY,raw TCP:host:port`)。

- 菜单 **操作 → 虚拟串口映射**(⌘⇧V)打开管理窗口
- 填 IP + 端口 → **添加映射**,生成设备如 `/tmp/CommBox-vserial-<PID>-1`(设备名带进程 PID,多实例不会撞名)
- **后台常驻,可同时多个**,与主连接互不影响
- **自动重连**:被桥接的服务端空闲断开后,设备保留并自动重连
- 在"串口"模式点刷新,列表会包含这些虚拟串口设备,可直接打开
- 用法:`screen /tmp/CommBox-vserial-<PID>-1 115200`,或用另一个串口工具/本工具第二实例打开

macOS/Linux 使用 PTY，无需额外驱动。Windows 不提供 TCP→虚拟串口创建入口，可使用下面的 VirtualCOM 免驱动端口。

### Windows：连接 VirtualCOM 免驱动端口

先运行独立的 `VirtualCOM-GUI.exe` 创建一对端口，例如 COM10 ⇄ COM11，并保持程序运行。在两个 CommBox 窗口中分别选择「串口 → 刷新串口」，打开 COM10 和 COM11，即可双向收发。命令行的 `-list` 和 `-port COM10` 同样支持这些端口。

连接后界面显示「免驱动」和「串口参数不生效」。VirtualCOM 传输原始字节，不实现波特率、数据位、校验、停止位及控制信号。此适配只解决 CommBox 与 VirtualCOM 的通信，未适配的第三方串口软件仍无法直接使用它。关闭 VirtualCOM 会断开通信；重新创建串口对后需在 CommBox 重新连接。

物理串口及驱动提供的 COM 口仍使用原串口库。使用步骤与实现说明见 [VirtualCOM 适配说明](docs/virtualcom-commbox-compat.md)。

## 数据存储

每条发送、接收、断开事件都写入 SQLite,**一条报文一条记录**:

| `source` | 内容 |
|---|---|
| `发送` | 发出的报文 |
| `串口`/`TCP …`/`UDP …`/`HTTP …` | 收到的报文 |
| `断开` | 被动断开事件 |

`raw_data` 保存无损 BLOB,`text_data` 保存 UTF-8 文本,`received_at` 为毫秒时间戳。数据库位于 `~/Library/Application Support/CommBox/data`(Windows 为对应 `%AppData%`),按日期和 100 MiB 自动滚动分文件。监控窗口可直接打开数据目录。

## 快捷键(macOS)

| 快捷键 | 功能 |
|---|---|
| ⌘L | 连接 / 断开 |
| ⌘↩ | 发送一次 |
| ⌘T | 定时发送开关 |
| ⌘R | 刷新串口 |
| ⌘⇧V | 虚拟串口映射 |
| ⌘⇧D | 打开数据库目录 |
| ⌘K | 清空接收区 |
| ⌘E | 导出接收数据 |
| ⌘⇧H | HEX 显示开关 |
| ⌘⇧M | 监控窗口 |
| ⌘X/C/V/A | 剪切 / 复制 / 粘贴 / 全选 |
| ⌘Q | 退出 |

## 构建

本仓库分别提供 Windows、macOS 和 Linux 构建产物，按各平台的构建与验证流程发布。

Windows 发布包（两个 CommBox 程序、两个 VirtualCOM 程序、说明文档与 SHA256SUMS）由 `scripts/build-release.ps1` 生成，版本号从源码常量读取：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1
```

脚本要求工作区干净且 HEAD 落在 `v<版本>` 标签上，逐个校验 PE 头、构建来源 commit、版本资源和压缩包内容。改版本号时同时改各目录的 `versioninfo.json` 并重新生成 `.syso`（`go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.5.0 -64 -o rsrc_windows_amd64.syso versioninfo.json`，新版 goversioninfo 生成的文件大一倍），漏改会有测试报错。

需要减小 Windows 下载包时，可在生成完整 ZIP 后执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/compress-windows.ps1 -SourceArchive build/CommBox-<版本>-Windows-x64.zip
```

脚本使用支持 7zip / LZMA2 的 `tar.exe`，在原 ZIP 旁生成完整 `.7z`，以及仅含 `CommBox.exe`、使用说明和校验清单的 `-GUI.zip` / `-GUI.7z`。只使用主界面时可选 GUI 包；需要命令行或创建 VirtualCOM 端口时选完整包。脚本核对源包清单，并逐文件验证新包解压后的 SHA256。此操作减小下载体积，解压后的 EXE 大小不变；沿用现有编译参数，不剥离符号或给 EXE 加壳。

```bash
# 命令行(多平台,纯 Go);发布 Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -X main.version=<版本>' -o commbox-linux-amd64 .

# Windows 桌面版(可交叉编译)
# 不要加 -s:剥符号的 GUI 程序在装了 360 的机器上会被当成加壳投放器隔离,
# 只用 -w 去掉调试信息,体积约减 25%
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags='-H windowsgui -w' -o build/windows/CommBox.exe ./windows

# macOS 桌面版(需在 macOS 上用 CGo 构建;按芯片分别出包,-s -w 瘦身)
# 对每个 ARCH ∈ {arm64(M 芯片), amd64(Intel)}：
for ARCH in arm64 amd64; do
  APP="build/$ARCH/CommBox.app"
  mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
  cp desktop/Info.plist "$APP/Contents/Info.plist"
  cp desktop/resources/CommBox.icns "$APP/Contents/Resources/CommBox.icns"
  CGO_ENABLED=1 GOARCH=$ARCH go build -trimpath -ldflags='-s -w' -o "$APP/Contents/MacOS/CommBox" ./desktop
  codesign --force --deep --sign - "$APP"
  # 直接分发软件：打成 dmg(双击挂载即用,拖入 Applications),无需解压
  ln -sf /Applications "build/$ARCH/Applications"
  NAME=$([ "$ARCH" = arm64 ] && echo AppleSilicon || echo Intel)
  hdiutil create -volname CommBox -srcfolder "build/$ARCH" -ov -format UDZO "CommBox-<版本>-macOS-$NAME.dmg"
done
```

运行测试:`go test ./...`
