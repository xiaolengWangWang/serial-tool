# CommBox —— 通信调试工具

一个跨平台的串口与网络调试工具:命令行 + macOS / Windows 原生桌面版,共享同一套核心引擎(`internal/wincore`)。支持串口、TCP/UDP 服务端与客户端、串口↔网络透传、HTTP 客户端、虚拟串口,以及全量 SQLite 收发存档。

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

- 收发区**每条数据带毫秒时间戳**,并区分 `发送` / `接收`
- **HEX 发送默认开启**,可切换;HEX 显示可切换
- 连接被远端断开时**按钮/状态自动同步**回未连接
- **历史连接**下拉:从数据库读最近 5 个配置,选中自动回填
- 连接状态显示模式、地址、串口参数、开始时间
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

> Windows 无 PTY,暂不提供虚拟串口。

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
| ⌘K | 清空接收区 |
| ⌘E | 导出接收数据 |
| ⌘⇧H | HEX 显示开关 |
| ⌘⇧M | 监控窗口 |
| ⌘X/C/V/A | 剪切 / 复制 / 粘贴 / 全选 |
| ⌘Q | 退出 |

## 构建

```bash
# 命令行(多平台,纯 Go)
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=0.3.0' -o commbox .

# Windows 桌面版(可交叉编译)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -H windowsgui' -o build/windows/CommBox.exe ./windows

# macOS 桌面版(需在 macOS 上用 CGo 构建)
mkdir -p 'build/CommBox.app/Contents/MacOS'
cp desktop/Info.plist 'build/CommBox.app/Contents/Info.plist'
go build -o 'build/CommBox.app/Contents/MacOS/CommBox' ./desktop
codesign --force --deep --sign - 'build/CommBox.app'
```

运行测试:`go test ./...`
