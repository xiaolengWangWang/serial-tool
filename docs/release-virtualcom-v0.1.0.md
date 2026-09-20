# VirtualCOM v0.1.0

Windows x64，解压后运行 `VirtualCOM.exe`，无需安装，不需要管理员权限。

自研虚拟串口，纯用户态实现：不安装驱动、不需要任何签名、不依赖第三方组件。与 CommBox 版本号各走各的。

## 本版内容

- 虚拟串口对的创建、删除、改号、启用/禁用、重启，支持自动选号与手动指定，支持多组并行。
- 端口状态四态（空闲 / 使用中 / 禁用 / 异常），显示占用程序名与 PID，取不到时明确报原因而不是当作空闲。
- 双向字节流收发，收发各 256KB 独立缓冲；缓冲写满时阻塞等待，不静默丢数据。
- TX / RX 字节数、读写次数、错误与缓存用量统计，可单独清空统计或清空缓存。
- 诊断报告：配对关系、端口占用、缓存与统计、系统 COM 占用情况，可直接复制。
- `cleanup` 命令清理进程异常退出后残留的 COM 编号。

命令：

```
VirtualCOM.exe run [COM10:COM11 ...]   创建串口对并常驻，Ctrl+C 退出并清理
VirtualCOM.exe ports                   列出系统当前已占用的 COM 编号
VirtualCOM.exe selftest                自检
VirtualCOM.exe cleanup                 清理残留 COM 编号
VirtualCOM.exe version                 显示版本
```

## 兼容性边界（请先读这一段）

本版把虚拟端口实现为**字节流管道**，不是真正的串口设备。以下为真机实测结果：

**能用**：`CreateFile` 打开、`ReadFile` / `WriteFile` 收发、Overlapped 异步 I/O、`CancelIoEx` 取消、跨进程打开、占用程序与 PID、端口重复打开关闭、多组互不串扰。

**不能用**：`GetCommState` / `SetCommState` / `SetCommTimeouts` / `SetCommMask` / `WaitCommEvent` / `PurgeComm` / `ClearCommError` 全部返回 `ERROR_INVALID_FUNCTION`；`GetFileType` 返回 `FILE_TYPE_PIPE` 而非 `FILE_TYPE_CHAR`；无波特率、数据位、校验、停止位设置；无 RTS / CTS / DTR / DSR / DCD / BREAK；设备管理器与 `SerialPort.GetPortNames()` 不显示这些端口。

**直接后果**：凡是用 .NET `SerialPort`、pyserial、`go.bug.st/serial` 打开端口的软件，会在打开阶段直接失败。只有把端口当字节流读写的程序能配合本版使用。

原因是内核里的命名管道驱动不处理串口 IOCTL，用户态代码无法改变这一点。要消除上述限制必须安装内核驱动，而 Windows x64 只加载带可信签名的驱动——这与本版「不装驱动、不签名」的选型互斥。

## 校验

```
b277329347bb683636ca9dba8909042c02209a3484e0786aae90a3a275f21ff6  VirtualCOM-0.1.0-Windows-x64.zip
2d3d0f76766e819eaacb2163dac4695576e0adfc81a8db7520082c6c475d4b39  VirtualCOM.exe
```

## 注意

- **进程退出后虚拟端口随即拆除**，通信中断。GUI 退出后继续工作、系统重启后自动恢复，本版均未实现。
- 进程被强杀时 COM 编号会残留在系统设备映射里，表现为「编号被占着但打不开」，用 `VirtualCOM.exe cleanup` 清理。
- 本版没有图形界面，只有命令行与库 API。
- 仅在 Windows 11 x64 上完成验收，Windows 10 x64 尚未验证。
- 未做长时间运行测试。
- 程序未做代码签名，SmartScreen 可能提示未知发布者。
