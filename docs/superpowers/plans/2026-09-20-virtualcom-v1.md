# VirtualCOM V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现独立 Windows 虚拟串口对管理工具，支持规格中的创建、管理、状态、诊断及标准串口兼容能力。

**Architecture:** UMDF2 驱动为每个端点提供真实 COM 设备、收发缓存及串口 API。Go 后台服务拥有持久化配对配置、设备生命周期和两个方向的转发；原生 Windows GUI 通过本机管理接口操作服务，退出 GUI 不结束通信。设备的标准串口句柄、服务专用后端句柄与只读观察句柄具有独立角色。

**Tech Stack:** Go 1.26、现有 Walk / Win32、Windows UMDF2 C 驱动、MSVC、Windows SDK / WDK 10.0.26100。

**Spec:** [VirtualCOM V1 功能规格](../specs/2026-09-20-virtualcom-v1-functional-spec.md)

## Global Constraints

- V1 只包含虚拟串口对；不包含 TCP/UDP 转串口、物理串口转发或串口助手。
- Windows 10 x64、Windows 11 x64，GUI 退出后 COM 继续工作。
- 缓冲区满时等待，尊重超时与取消；不得静默丢弃已接受数据。
- 修改、禁用、重启、删除前必须检查占用；不结束第三方进程。
- 没有真实驱动就报告不可用，禁止用内存端口、注册表名称或测试替身冒充 COM。
- 现有 CommBox 和 macOS/Linux PTY 功能保持可构建；新增工具单独出包。
- 驱动安装、证书信任和系统重启属于测试机环境操作；交付可审阅产物后再进行相应授权操作。

## Review Focus

1. 用户输入的 COM 名称存在大小写、前导零、边界值或重复，必须标准化并拒绝冲突；Task 2 覆盖。
2. 创建第二端、改号或删除中途失败，必须保留可恢复的记录，不报告完整成功；Task 3 覆盖。
3. 读写取消恰逢数据到达，不能双重完成、错报字节数或跨端口串流；Task 1、6 覆盖。
4. 普通串口软件使用独占打开时，后台转发仍可工作，普通进程不能冒充后端注入数据；Task 1、6 覆盖。
5. 驱动或服务停止、数据已排队、GUI 同时刷新时，界面不能显示虚假空闲或零统计；Task 3、4、6 覆盖。

## 文件边界与协作契约

| 路径 | 职责 |
| --- | --- |
| `virtualcom/driver/` | UMDF2 驱动源码、INF、工程、驱动级测试 |
| `virtualcom/include/virtualcom_ioctl.h` | 驱动与 Go 共享的字节级协议定义 |
| `internal/virtualcom/api.go` | GUI / 服务的数据类型与 Client 接口 |
| `internal/virtualcom/` 其余文件 | 配对管理、配置存储、原生设备访问、服务 IPC 和诊断 |
| `cmd/virtualcom/` | 独立 Walk 主界面和详情、创建、诊断窗口 |
| `cmd/virtualcom-service/` | 后台 Windows 服务、命令行安装与维护入口 |
| `virtualcom/scripts/` | 构建、打包、测试环境准备与安装脚本 |
| `virtualcom/tests/` | 使用真实 Win32 API 的端到端串口验证 |

固定协议和 API 见 [实现契约](2026-09-20-virtualcom-contract.md)。驱动、管理模块和 GUI 分别修改各自目录；共享契约由主实现者维护。

## Task 1：UMDF2 真实端点与串口兼容

**Files:** `virtualcom/driver/{driver.c,device.c,serial.c,queues.c,driver.h,VirtualCOM.vcxproj,VirtualCOM.inf}`、`virtualcom/tests/serial_test_windows.go`。

**Interfaces:** Consumes `virtualcom/include/virtualcom_ioctl.h`；produces hardware ID `ROOT\\VIRTUALCOM` 与契约内串口/后端/观察接口。

- [ ] 编写真实端口行为测试，用环境变量 `VIRTUALCOM_TEST_PORT_A` / `VIRTUALCOM_TEST_PORT_B` 指定专用测试端口。未指定时明确跳过；指定但打不开时失败，不能自动降级。

```go
a := openSerial(t, os.Getenv("VIRTUALCOM_TEST_PORT_A"))
b := openSerial(t, os.Getenv("VIRTUALCOM_TEST_PORT_B"))
writeAll(t, a, []byte{0, 1, 0x80, 0xff})
if got := readExactly(t, b, 4); !bytes.Equal(got, []byte{0, 1, 0x80, 0xff}) {
    t.Fatalf("bytes changed: %x", got)
}
```

- [ ] 先执行测试，记录真实驱动未安装的限制；编写可在无设备环境运行的环形缓冲区与超时边界测试并观察失败。
- [ ] 实现独占串口客户端、后端身份检查、RX/TX 缓冲、读写超时、取消、串口参数、信号、等待掩码、Purge 和统计。所有被移出的 WDF 请求恰好完成一次；停机时取消等待请求。
- [ ] 构建驱动并运行原生逻辑测试；在专用环境执行真实 COM 测试后才能声明通信通过。

Run: `virtualcom/scripts/build-driver.ps1`；`go test ./virtualcom/tests -count=1 -v`。

Expected: 编译无错误；逻辑测试通过；真实端口测试逐项显示 pass 或准确的环境缺口。

## Task 2：编号、配置与管理模型

**Files:** `internal/virtualcom/{api.go,ports.go,manager.go,store.go,manager_test.go,store_test.go}`。

**Interfaces:** Consumes `Client` / `Command` / `Snapshot` 契约；produces manager 的 `Snapshot`、`Apply` 和 `Diagnostics` 行为。

- [ ] 先写编号标准化、重复编号、外部冲突、第二端创建失败、使用中禁止删除和重启恢复的测试。

```go
for _, input := range []string{"COM0", "COM-1", "COM1/../../x", "COM4097"} {
    if _, err := NormalizePort(input); err == nil { t.Fatalf("accepted %q", input) }
}
if got, err := NormalizePort(" com010 "); err != nil || got != "COM10" {
    t.Fatalf("normalize = %q, %v", got, err)
}
```

- [ ] 运行失败测试，随后实现 COM1..COM4096 编号规则、配置原子写入、所有权校验、逐对操作事务及部分失败记录。
- [ ] 使用真实临时配置文件验证重读、损坏配置拒绝、并发请求串行化；操作系统设备操作仅在这一边界使用可控测试替身。

Run: `go test ./internal/virtualcom -count=1`。

Expected: 业务约束和持久化测试全部通过，不能把替身测试标为驱动验收。

## Task 3：Windows 设备管理、后台服务与转发

**Files:** `internal/virtualcom/{native_windows.go,backend_windows.go,ipc_windows.go,service_windows.go}`、`cmd/virtualcom-service/main_windows.go`。

**Interfaces:** Consumes 共享驱动协议与 Task 2 管理模型；produces `NewClient() Client` 与实际 Windows 服务 `VirtualCOM`。

- [ ] 先测试协议字节解码（截断、错误版本、超大长度）、请求权限、并发取消和短写处理。
- [ ] 使用 SetupAPI / ConfigMgr 管理本工具拥有的设备，使用 COM 数据库分配编号，使用 SCM 管理独立服务。设备身份绑定持久化 UUID；不以任意用户给出的实例路径作为删除目标。
- [ ] 两个方向分别运行有界转发，处理短写、超时与服务停止；传播 RTS/DTR/BREAK 和端点状态。后台句柄通过独立引用字符串打开并授权。
- [ ] 本机 IPC 只允许受信任的管理客户端修改设备；读取未知状态必须保留 unknown/error。配置和运行状态使用机器级目录，GUI 生命周期不拥有端口。
- [ ] 完成命令行 `install-service`、`uninstall-service`、`status`、`serve`，所有高权限操作返回真实错误。

Run: `go test ./internal/virtualcom -count=1`；`go build ./cmd/virtualcom-service`。

Expected: 协议、授权和业务测试通过；服务可构建；未安装驱动时返回明确 unavailable。

## Task 4：独立原生 GUI

**Files:** `cmd/virtualcom/{main_windows.go,dialogs_windows.go,viewmodel.go,viewmodel_test.go}`、原生 manifest 资源。

**Interfaces:** Consumes `virtualcom.NewClient() Client`；所有设备变更通过 `Apply`，UI 不直接改注册表或自行管理转发。

- [ ] 先测试状态映射与未知值显示，确认错误信息不会变成“空闲”或 0。
- [ ] 按规格创建卡片主界面，提供自动/手动创建、详情和更多菜单。增加改号、启停、重启、删除、清空缓存/统计、诊断复制与驱动管理窗口。
- [ ] 状态读取和操作放到后台，关闭窗口后安全停止 UI 回调；操作中的卡片防重复提交，失败后保留错误。
- [ ] 使用真实 Walk 控件测试基本交互，并构建独立 `VirtualCOM.exe`。

Run: `go test ./cmd/virtualcom -count=1`；`go build -ldflags='-H windowsgui -s -w' -o build/virtualcom/VirtualCOM.exe ./cmd/virtualcom`。

Expected: UI 状态逻辑通过、EXE 构建成功；驱动不可用时界面准确显示原因。

## Task 5：构建与安装交付

**Files:** `virtualcom/scripts/{build-driver.ps1,build.ps1,install.ps1,uninstall.ps1}`、`virtualcom/README.md`。

**Interfaces:** Consumes 驱动工程、Go 两个入口与签名后的驱动包；produces EXE、服务 EXE、DLL/INF/CAT 和清晰的测试状态。

- [ ] 固定 SDK/WDK 版本，校验本机 MSVC/WDK 工具位置，缺失即报错。
- [ ] 构建脚本逐个检查退出码；安装脚本校验目标目录、管理员权限、签名与设备身份；失败时报告已完成和待恢复的操作。
- [ ] 在无驱动时构建 GUI 与服务，在工具链具备后构建驱动。产物清单分开记录“已构建”“已签名”“已安装测试”，不相互替代。

Run: `virtualcom/scripts/build.ps1`。

Expected: 本机能够验证的步骤完成；工具链、证书或测试机缺口原样报告。

## Task 6：验收与代码审查

- [ ] 执行 `go test ./... -count=1`，所有失败都记录并处理。
- [ ] 执行驱动逻辑测试与编译，核对共享 C/Go 协议的固定尺寸与字段偏移。
- [ ] 执行第三方客户端、全双工、连续大数据、多组隔离、超时/取消、流控、占用和恢复测试；驱动未安装时不假定通过。
- [ ] 对整个变更进行独立审查，修复影响功能、数据完整性或权限边界的发现。
- [ ] 维护 F01..F14 验收表，附命令、结果与未完成原因；只有真实验收通过的功能才标记支持。

## 当前执行状态

用户已要求“实现”。工作在 `.worktrees/virtualcom-v1` / `feat/virtualcom-v1` 中，后续实现按上述边界推进。所有任务的证据和环境限制记录到 `virtualcom/IMPLEMENTATION.md`。
