Go 网络与串口工具（Windows 64 位）

1. 双击 GoSerialTool.exe 运行，无需安装。
2. TCP/UDP 服务端首次监听时，请允许 Windows 防火墙访问。
3. USB 串口需要安装设备厂商提供的 Windows 驱动。
4. SQLite 数据保存在：%AppData%\GoSerialTool\data
5. 当前构建未使用商业代码签名证书，Windows SmartScreen 可能显示未知发布者。

支持：
- 串口文本/HEX 收发及常用串口参数
- TCP/UDP 客户端与服务端
- 串口服务器双向透明透传
- 接收日志导出、定时发送和独立监控窗口
- 每条原始接收数据保存为 SQLite BLOB 和 UTF-8 字符串
- SQLite 按日期和 100 MiB 自动分文件

提示：TCP 客户端出现 connection refused，表示目标 IP:端口没有服务端监听。
