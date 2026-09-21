# VirtualCOM 实现记录

计划：`docs/superpowers/plans/2026-09-20-virtualcom-v1.md`

契约：`docs/superpowers/plans/2026-09-20-virtualcom-contract.md`

规格：`docs/superpowers/specs/2026-09-20-virtualcom-v1-functional-spec.md`

## 当前状态（2026-09-21）

规格里的 V1（UMDF2 驱动路线）**未实现**：没有编写、构建、签名或安装任何驱动，本机也没有可用的 MSVC / SDK / WDK。规格第 6 节的 F01..F14 一项都没有通过真实验收，因此不标记为已支持。

实际交付的是另一条路线：用户态免驱动 VirtualCOM（独立模块 `virtualcom/`，v0.2.0）加 CommBox v0.9.0 的连接适配。它用命名管道和 DOS 设备别名提供字节流端口，不提供标准串口 API，不满足规格 2.4 / 2.5 / 2.6 节。

## 免驱动路线

已完成：

- `virtualcom` v0.1.0 / v0.2.0：命令行与图形界面，创建和删除串口对，自动挑选空闲编号，自检，进程异常退出后的残留清理。
- CommBox v0.9.0：串口列表合并当前会话内的 VirtualCOM 活动端口；收发使用重叠 I/O；关闭时先停止提交再取消并等待未完成请求；界面显示「免驱动」及参数不生效；修复刷新丢失端口选择与历史连接回填导致参数无效。
- 验证：Windows 11 build 26200，两组真实配对、四个方向各 1 MiB 并发传输、全部字节值、反复关闭重开、退出清理；根模块与 `virtualcom` 模块 `go test ./... -count=1` 全部通过；完成一次独立代码审查。

未完成：

- 标准串口 API 不生效：波特率、数据位、校验、停止位、RTS / CTS、DTR / DSR、DCD、BREAK、硬件流控、`WaitCommEvent`、`PurgeComm`。未适配的第三方串口软件无法使用这些端口。
- 端口随 VirtualCOM 进程存在：GUI 退出后通信不继续，系统重启后不恢复，设备管理器中不可见。
- 驱动管理、跨用户登录会话、Windows 10 x64 验收、长时间运行验收均未做。

## 已移除的草稿

`internal/virtualcom/api.go` 和 `virtualcom/include/virtualcom_ioctl.h` 是驱动路线的接口草稿，没有被任何代码引用。为避免与实际交付混淆，两者已删除；驱动路线的接口定义保留在上面的计划与契约文档中。

## 其它证据

- 2026-09-20 基线：`go test ./... -count=1` 在根模块、wincore 与既有 Windows UI 包通过；该基线未启用交互式 GUI 与在线更新测试。
- 从 Microsoft 官方分发地址下载了 Visual Studio Build Tools 引导程序，Authenticode 签名验证为 Valid / Microsoft Corporation；标准安装位置下没有找到已有的 MSVC / SDK / WDK。

## 决策

- 开发使用独立工作树 `.worktrees/virtualcom-v1`，分支 `feat/virtualcom-v1`。
- 驱动安装、证书信任和启动策略修改属于测试机环境操作，本仓库没有做过任何此类改动。
