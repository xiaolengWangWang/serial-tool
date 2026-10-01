# CommBox 对 VirtualCOM 的免驱动适配

CommBox 通过 Go 模块直接集成 [VirtualCOM](https://github.com/xiaolengWangWang/virtualcom) 的串口核心与原生管理页。Windows 发布包也附带独立工具，适合单独管理端口。

## 使用

1. 在 CommBox 选择「工具 → 虚拟串口管理」，填写端口 A / B 并创建；两项留空时自动分配空闲编号。无需启动其他程序。
2. 打开两个 `CommBox.exe` 窗口，分别选择「串口 → 刷新串口」，各自连接这一对的一个端口。
3. 一端发送，另一端接收；可使用 HEX、文本、定时发送及现有数据记录功能。
4. 管理页可查看占用程序、双向传输量，选中串口对后启停或删除；使用中的端口会拒绝破坏性操作并显示原因。
5. 关闭管理窗口后端口继续工作，再次打开保留原串口对。退出创建端口的 CommBox 实例时会释放端口；其他实例会断开，需重新创建并连接。

命令行示例（以实际创建的 COM 号为准）：

```powershell
.\CommBox-CLI.exe -list
.\CommBox-CLI.exe -port COM10 -hex -hex-send
```

本功能使用 VirtualCOM 字节流端口。没有安装驱动、修改签名策略，也没有赋予命名管道标准串口 API。波特率、数据位、校验、停止位、RTS/DTR 等控制信号不生效，未适配的第三方软件仍不兼容。创建端口的程序与使用端口的程序必须位于同一用户登录会话。

## 实现与验证

- 合并物理串口列表与当前会话内的 VirtualCOM 活动端口；读取设备映射与管道目录，不打开端口探测占用。
- 仅识别目标形如 `\Device\NamedPipe\VirtualCOM-<PID>-<序号>` 的 COM 映射；其他设备继续交给原串口库。失效的 VirtualCOM 别名不加入列表。
- 收发分别使用重叠 I/O；关闭时先停止提交，取消并等待未完成请求，再释放句柄。写入返回实际完成字节数，取消不冒充成功。
- 引擎、命令行、串口服务器共享适配入口。接收数据继续无损写入 SQLite；连接记录标识 `backend=VirtualCOM`，不宣称串口设置已生效。

验证命令：

```powershell
$env:COMMBOX_VIRTUALCOM_TEST='1'
$env:COMMBOX_GUI_TEST='1'
go test ./... -count=1 -timeout=120s
```

普通测试创建临时高位 COM 别名及真实 Windows 管道，结束后按精确目标移除。启用 `COMMBOX_VIRTUALCOM_TEST` 时，另外构建并启动独立 VirtualCOM 测试进程，使用真实配对实现验证两组同时运行、四个方向各 1 MiB、全部字节值、反复关闭重开及退出清理。未启用时，该项明确显示为跳过。GUI 测试需要交互式 Windows 桌面。

以上测试不等同于 Windows 10 或长时间运行验收，也不代表未适配串口软件可用。
