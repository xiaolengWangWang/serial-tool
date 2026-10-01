CommBox（Windows 64 位）

1. 双击 CommBox.exe 运行，无需安装。
2. TCP/UDP 服务端首次监听时，请允许 Windows 防火墙访问。
3. USB 串口需要安装设备厂商提供的 Windows 驱动。
4. SQLite 数据保存在：%AppData%\CommBox\data
5. 当前构建未使用商业代码签名证书，Windows SmartScreen 可能显示未知发布者。

支持：
- 串口文本/HEX 收发及常用串口参数
- TCP/UDP 客户端与服务端
- 串口服务器双向透明透传
- 接收日志导出、定时发送和独立监控窗口
- 断开时日志写明是本端、服务端还是客户端断的,附断开方式、时长和收发量
- 每条原始接收数据保存为 SQLite BLOB 和 UTF-8 字符串
- SQLite 按日期和 100 MiB 自动分文件

VirtualCOM（内置管理及独立工具，v0.2.2）：
1. 在 CommBox 的「工具 → 虚拟串口管理」创建一对端口（例如 COM10 ⇄ COM11），留空自动选择空闲编号。
2. 打开两个 CommBox 窗口，在「串口」模式点刷新，分别连接这一对的一端，即可双向收发。
3. 命令行：VirtualCOM.exe run 创建串口对，CommBox-CLI.exe -list 查看端口，-port COM10 打开。
4. 免驱动端口只传输原始字节。波特率、数据位、校验、停止位及 RTS/DTR 等控制信号不生效，
   连接后状态栏会显示「免驱动」。
5. 管理页支持查看状态及收发量、启停和删除。关闭管理窗口后端口继续工作，退出创建它的 CommBox 时释放。
6. VirtualCOM 需与 CommBox 处于同一用户登录会话；未适配的第三方串口软件仍无法使用这些端口。
7. 也可单独运行 VirtualCOM-GUI.exe 管理端口；两种界面共用同一套代码。

详细说明见 VirtualCOM使用说明.md。

提示：TCP 客户端出现 connection refused，表示目标 IP:端口没有服务端监听。
