# CommBox 对 VirtualCOM 的免驱动适配

日期：2026-09-21。CommBox v0.9.0，Windows 发布包附带 VirtualCOM v0.2.0。

## 使用

1. 打开 `VirtualCOM-GUI.exe`，创建一对空闲端口，保持程序运行。
2. 打开两个 `CommBox.exe` 窗口，分别选择「串口 → 刷新串口」，各自连接这一对的一个端口。
3. 一端发送，另一端接收；可使用 HEX、文本、定时发送及现有数据记录功能。
4. 状态栏显示「免驱动」及参数不生效的说明。关闭连接后可再次打开；VirtualCOM 退出后需重新创建端口并重新连接。

命令行示例（以实际创建的 COM 号为准）：

```powershell
.\CommBox-CLI.exe -list
.\CommBox-CLI.exe -port COM10 -hex -hex-send
```

本功能仅让 CommBox 兼容现有 VirtualCOM 字节流端口。没有安装驱动、修改签名策略，也没有赋予命名管道标准串口 API。波特率、数据位、校验、停止位、RTS/DTR 等控制信号不生效，未适配的第三方软件仍不兼容。VirtualCOM 必须与 CommBox 位于同一用户登录会话。

## 实现与验证

- 合并物理串口列表与当前会话内的 VirtualCOM 活动端口；读取设备映射与管道目录，不打开端口探测占用。
- 仅识别目标形如 `\Device\NamedPipe\VirtualCOM-<PID>-<序号>` 的 COM 映射；其他设备继续交给原串口库。失效的 VirtualCOM 别名不加入列表。
- 收发分别使用重叠 I/O；关闭时先停止提交，取消并等待未完成请求，再释放句柄。写入返回实际完成字节数，取消不冒充成功。
- 引擎、命令行、串口服务器共享适配入口。接收数据继续无损写入 SQLite；连接记录标识 `backend=VirtualCOM`，不宣称串口设置已生效。
- 原来的驱动方案草稿不属于本次实现范围。

验证命令：

```powershell
$env:COMMBOX_VIRTUALCOM_TEST='1'
$env:COMMBOX_GUI_TEST='1'
go test ./... -count=1 -timeout=120s
```

普通测试创建临时高位 COM 别名及真实 Windows 管道，结束后按精确目标移除。启用 `COMMBOX_VIRTUALCOM_TEST` 时，另外构建并启动独立 VirtualCOM 测试进程，使用真实配对实现验证两组同时运行、四个方向各 1 MiB、全部字节值、反复关闭重开及退出清理。未启用时，该项明确显示为跳过。GUI 测试需要交互式 Windows 桌面。

此次验证不等同于 Windows 10 或长时间运行验收，也不代表未适配串口软件可用。

2026-09-21 验证结果：Windows build 26200，以上两个开关均开启后的根模块全量测试通过，包括跨进程 VirtualCOM 配对和真实 Windows 控件测试。专用测试曾先复现端口缺失与 `Invalid serial port`，修复后通过；历史连接回填测试曾先复现「波特率无效」，修复后通过。现有独立 VirtualCOM 模块测试通过。另完成一次独立代码审查，未发现重要或严重问题。
