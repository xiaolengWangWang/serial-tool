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

- v0.9.9：对端关闭/监听出错后不再多记一条“用户在本程序上断开”;虚拟串口数据跨数据库文件轮换(跨零点或满 100 MB)后仍归属自己的会话,不再丢失或串到别的连接;拨号中途移除虚拟串口不再遗留连接。VirtualCOM 0.2.1(待 Windows 发布):“清空 Buffer”连同已取出待送达的数据一并丢弃
- v0.9.8：启动自动检查更新只在新版本带本平台安装包时弹窗(Windows/macOS 不再被对方平台的发布打扰);HTTP 工作区用完即关临时连接池,响应正文上限 64 MiB 超出截断并提示;cURL 导入修复 `-d'a=b'`/`-H'X: a=b'` 连写解析,支持 `-sSL` 组合选项、`--json`,忽略 `--compressed`/`-s`/`-S`/`-i`/`-v`
- macOS v0.9.7：修复 HTTP 工作区每次请求泄漏响应正文;监听报文切换 HEX/ASCII、改过滤时按字符上限只渲染最新部分(大包不再卡顿);下载更新中不能重复触发,进度回报节流,主动取消不再弹"失败";Info.plist 版本号与程序同步
- macOS v0.9.6：新增 **HTTP 工作区**(操作 → HTTP 工作区,⌘⇧U):方法/URL、总超时与连接超时、跟随重定向、跳过 TLS 证书校验,请求头与请求体编辑,cURL 导入与生成,响应正文/响应头分页与 JSON 格式化;**连接日志**缓冲限长、每行时间戳、错误/连接分级高亮,支持清空/复制/导出;**监听报文**批量刷新+限长(长跑不吃内存)、关键字过滤、HEX/ASCII 切换
- macOS v0.9.5：接入**在线更新**(帮助 → 检查更新):按芯片自动匹配 DMG、下载校验 SHA256 后自动打开,启动时自动检查默认开启可关闭;更新包后缀按平台拆分
- macOS v0.7.5：蓝白连接卡片、独立状态/累计收发/真实 TCP 对端信息，默认窗口 1280×820；连接失败不再停留在“正在连接”，UDP / HTTP 显示“就绪”而不是已连接
- 数据库分析支持数据目录内多个 SQLite 文件，⌘ / Shift 多选或全选；最新条数最高 1,000,000（所选文件合计）。完整筛选统计、8 MiB 选中负载上限、20 条详细解析；省略范围明确显示，不上传 AI
- macOS v0.7.3：修复“数据库分析”弹窗标题、说明与表单重叠，增加真实弹窗的跨容器布局回归检查
- macOS v0.7.2：分析中心默认隐藏，连接参数采用紧凑表单，筛选和选择只针对可见报文；[布局与验证说明](docs/CommBox_v0.7.2_布局修复.md)
- macOS 顶部“数据库分析”：按文件、时间、RX/TX 查询本地 SQLite，离线只读分析完整原始负载，报告明确列出条数/数据量限制；不自动调用 AI
- 收发区**每条数据带毫秒时间戳**,并区分 `发送` / `接收`
- **HEX 发送默认开启**,可切换;HEX 显示可切换
- 连接被远端断开时**按钮/状态自动同步**回未连接
- 断开时在日志里写明**是哪一端断的**:本端主动断开、服务端断开、客户端断开还是链路中断,并附断开方式(收到 FIN / 连接被重置 / 读取超时)、对端地址、本次连接时长、收发字节与帧数和底层错误;同一条报文也写入 SQLite
- **历史连接**下拉:从数据库读最近 5 个配置,选中自动回填
- 连接状态显示模式、地址、串口参数、运行时间；收发/重连/错误为本次应用运行累计
- 实时监控独立窗口、日志导出、定时发送(最小间隔 10 ms)

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

macOS/Linux 使用 PTY，无需额外驱动。Windows 的 TCP→虚拟串口创建入口仍未开放；CommBox v0.9.0 已加入下面的 VirtualCOM 免驱动连接适配。

### Windows：连接 VirtualCOM 免驱动端口

先运行独立的 `VirtualCOM-GUI.exe` 创建一对端口，例如 COM10 ⇄ COM11，并保持程序运行。在两个 CommBox 窗口中分别选择「串口 → 刷新串口」，打开 COM10 和 COM11，即可双向收发。命令行的 `-list` 和 `-port COM10` 同样支持这些端口。

连接后界面显示「免驱动」和「串口参数不生效」。VirtualCOM 传输原始字节，不实现波特率、数据位、校验、停止位及控制信号。此适配只解决 CommBox 与 VirtualCOM 的通信，未适配的第三方串口软件仍无法直接使用它。关闭 VirtualCOM 会断开通信；重新创建串口对后需在 CommBox 重新连接。

物理串口及驱动提供的 COM 口仍使用原串口库。实现、验证和试用步骤见 [VirtualCOM 适配记录](docs/virtualcom-commbox-compat.md)。

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

本 macOS 开发线仅同步和发布 macOS/Linux 构建产物；本工作流不修改、不构建或上传 Windows 产物。

Windows 发布包（两个 CommBox 程序、两个 VirtualCOM 程序、说明文档与 SHA256SUMS）由 `scripts/build-release.ps1` 生成，版本号从源码常量读取：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1
```

脚本要求工作区干净且 HEAD 落在 `v<版本>` 标签上，逐个校验 PE 头、构建来源 commit、版本资源和压缩包内容。改版本号时同时改各目录的 `versioninfo.json` 并重新生成 `.syso`（`goversioninfo -64 -o rsrc_windows_amd64.syso versioninfo.json`），漏改会有测试报错。

```bash
# 命令行(多平台,纯 Go);发布 Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -X main.version=0.7.8' -o commbox-linux-amd64 .

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
  hdiutil create -volname CommBox -srcfolder "build/$ARCH" -ov -format UDZO "CommBox-macOS-$NAME.dmg"
done
```

运行测试:`go test ./...`
