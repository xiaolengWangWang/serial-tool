# VirtualCOM 0.1.0

自研 Windows 虚拟串口。纯用户态实现：**只用 Win32 API，不安装驱动、不需要任何签名、不依赖任何第三方组件**。

独立项目，与 CommBox 各走各的版本号。并入 CommBox 后 CommBox 升到 0.9.0。

---

## 一、这个版本能做什么，不能做什么

先说边界，免得按串口软件的预期去用然后踩坑。以下结论**全部是在真机（Windows 11 Pro 26200 x64）上实测得出的**，不是推断。

### 能用

| 能力 | 实测结果 |
| --- | --- |
| `CreateFile("\\.\COM10")` 打开端口 | 成功，且**跨进程可见**（另一个进程能打开本进程建的端口） |
| `ReadFile` / `WriteFile` 字节流收发 | 双向、全双工、1MB 连续传输内容一致 |
| `0x00`~`0xFF` 全字节值穿透 | 原样通过，不被改写 |
| Overlapped 异步 I/O | 支持，且收发必须靠它（见第三节） |
| `CancelIoEx` 取消 | 支持 |
| 占用程序名与 PID | `GetNamedPipeClientProcessId` 可取，取不到时明确报原因 |
| 端口被占用时二次打开 | 失败并报「已被其它程序占用」 |
| 多组串口并行 | 三组同时传不同内容，互不串组 |

### 不能用

| 能力 | 实测结果 |
| --- | --- |
| `GetCommState` / `SetCommState` | `ERROR_INVALID_FUNCTION` |
| `GetCommTimeouts` / `SetCommTimeouts` | `ERROR_INVALID_FUNCTION` |
| `SetCommMask` / `WaitCommEvent` | `ERROR_INVALID_FUNCTION` |
| `PurgeComm` / `ClearCommError` | `ERROR_INVALID_FUNCTION` |
| `GetFileType` | 返回 `3 = FILE_TYPE_PIPE`，真串口是 `2 = FILE_TYPE_CHAR` |
| 波特率 / 数据位 / 校验 / 停止位 | 无法设置或读取 |
| RTS / CTS / DTR / DSR / DCD / BREAK | 无 |
| 设备管理器、`SerialPort.GetPortNames()` | 不显示 |

### 这对第三方软件意味着什么

**凡是用 .NET `SerialPort`、pyserial、`go.bug.st/serial` 打开端口的程序，会在打开阶段直接失败。**

实测 .NET：

```
System.IO.Ports.SerialPort("COM90").Open()
→ The given port name does not start with COM/com or does not resolve to a valid serial port.
```

.NET 在调 DCB 之前先查 `GetFileType`，命名管道过不了这一关，连门都进不去。

**只有把端口当字节流读写的程序能用。**

### 为什么不能做到 100% 兼容

内核里的命名管道文件系统驱动根本不处理串口 IOCTL，**用户态代码没有任何办法改变这一点**。要让 `GetCommState` 之类的 API 工作，端口必须由一个真正的串口驱动提供；而 Windows x64 内核只加载带可信签名的驱动，没签名的加载失败（错误 577 `ERROR_INVALID_IMAGE_HASH`）。

注意开源不解决签名问题：签名绑定的是**二进制文件**，不是源代码。自己编译出来的 `.sys` 是全新文件、零签名，照样加载不了。

所以「100% 兼容」和「不装驱动、不签名」在 Windows 上互斥。本项目选择了后者，代价就是上面那张表。

---

## 二、用法

```
virtualcom run [COM10:COM11 ...]   创建串口对并常驻，Ctrl+C 退出并清理
                                   不带参数则自动挑两个空闲编号
virtualcom ports                   列出系统当前已占用的 COM 编号
virtualcom selftest                自检：双向、全双工、全字节值、重复开关
virtualcom cleanup                 清理进程异常退出后残留的 COM 编号
virtualcom version                 显示版本
```

构建与验证：

```
go build ./cmd/virtualcom
go test ./...
go run ./cmd/virtualcom selftest
```

**进程退出时会拆掉虚拟端口，通信随之中断。** 这是当前版本的已知限制，见第四节。

如果进程被强杀（崩溃、任务管理器结束、调试器中断），COM 名会留在系统设备映射里而管道已经没了，表现为「这个编号被占着，但任何程序打开它都报找不到」。`cleanup` 专门清这种残留，它只删目标指向本项目管道、且管道确实已不存在的链接，不碰别人的。

---

## 三、实现方式

架构直接对齐仓库里已有的 Linux/macOS 虚拟串口实现（`internal/wincore/vserial_unix.go`），两边是同构的：

| | Linux / macOS | Windows（本项目） |
| --- | --- | --- |
| 程序读写端 | `pty.Open()` 返回的 `ptmx` | 命名管道**服务端**句柄 |
| 串口软件打开的设备 | `/dev/pts/N` | `COM10` 符号链接 → `\Device\NamedPipe\VirtualCOM-<pid>-<seq>` |
| 稳定的用户可见名 | `/tmp/CommBox-vserial-*` 软链 | `DefineDosDevice` 建的 COM 名 |
| 拆除 | 关 ptmx/tty、删软链 | 关句柄、删符号链接 |

三件套都是 `master` / `link` / `close`。唯一的实质差别：Unix 那边软件端是内核 pts **字符设备**，所以 termios 全套都能用；这边是命名管道，所以串口专用 API 全军覆没。这就是第一节那张表的根因。

一对串口 = 两个设备 + 两条中继。每端各有一个收件环形缓冲（256KB）：

```
串口软件A --写--> [管道A服务端] --readLoop--> (B的收件缓冲) --writeLoop--> [管道B服务端] --读--> 串口软件B
```

缓冲写满时**阻塞等待**对端读走，不丢数据。本端暂时没有程序打开时，数据留在待发队列里等着，也不丢。

`DefineDosDevice` 未提权时把符号链接建在**当前登录会话的设备映射**里，同一登录用户的所有进程都能看到——已实测跨进程打开成功。所以不需要管理员权限。

### 一个必须记住的坑：句柄不能用同步模式

所有管道句柄（服务端和客户端）都必须带 `FILE_FLAG_OVERLAPPED`。

同步模式下，内核会把同一个句柄上的 I/O **串行化**：一个挂起的 `ReadFile` 会把同句柄上的 `WriteFile` 一起堵死——读在等对端发数据，写在等读让出句柄，双方互等，整对串口直接卡住。串口本来就是全双工，收发必须能同时挂起。

`TestPairFullDuplexNoDeadlock` 就是这个坑的回归测试，不能删。

---

## 四、对照功能规格的完成情况

对照 `docs/superpowers/specs/2026-09-20-virtualcom-v1-functional-spec.md`。

### 已完成并有测试覆盖

| 规格条目 | 状态 |
| --- | --- |
| 2.1 创建/删除/多组/自动选号/手动指定/改号/启停/重启 | 库 API 完成 |
| 2.2 四态、占用程序、PID、刷新 | 完成 |
| 2.3 双向、全双工、大数据、多组隔离 | 完成，实测通过 |
| 2.7 独立缓冲、满则等待、用量显示、清空 | 完成 |
| 2.8 TX/RX、读写次数、错误、清空统计 | 完成（Pending IO 数量未做） |
| 2.9 诊断报告（配对、占用、缓存、统计、可复制） | 完成（驱动检查项不适用） |
| 第 4 节 使用中拒绝删除/改号/禁用/重启 | 完成 |

### 受实现路线限制，做不到

| 规格条目 | 原因 |
| --- | --- |
| 2.4 串口参数兼容 | 命名管道不支持串口 IOCTL |
| 2.5 控制信号兼容 | 同上 |
| 2.6 中的 `*Comm*` 系列 API | 同上 |
| 2.10 驱动管理 | 本方案不装驱动，该章整体不适用 |
| 2.11 设备管理器可见 | 不是 PnP 设备 |
| F07 / F08 / F12 | 对应上述条目 |

### 尚未实现（不是做不到，是这一版还没做）

| 规格条目 | 说明 |
| --- | --- |
| 第 3 节 主界面 | 还没有 GUI，当前只有库 API 和 CLI |
| 2.11 GUI 退出后继续工作 | 需要常驻后台进程持有端口，尚未实现 |
| 2.11 系统重启后继续存在 | 需要配置持久化 + 开机自启，尚未实现 |
| 2.12 日志 | 未实现 |
| F13 Windows 10 x64 验收 | 只在 Windows 11 x64 上验证过 |
| F14 长时间运行 | 未做长跑测试 |

按规格第 6 节的要求：以上没有完成真机与第三方软件验收的项目，一律不标记为已支持。
