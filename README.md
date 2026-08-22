# Go 串口工具

```bash
go build -o serial-tool .

# 查看串口
./serial-tool -list

# 115200 8N1，文本收发
./serial-tool -port /dev/tty.usbserial-0001 -baud 115200 -eol crlf

# HEX 收发
./serial-tool -port /dev/tty.usbserial-0001 -baud 9600 -hex -hex-send
```

Windows 串口名称可写成 `COM3`。运行 `./serial-tool -h` 查看全部参数。

## macOS 原生桌面版

已构建应用位于 `build/Go 串口工具.app`，双击即可运行。界面支持串口、TCP/UDP 服务端和 TCP/UDP 客户端。服务端填写本机监听地址，客户端填写远程 `IP:端口`。TCP 服务端发送会广播到全部客户端，UDP 服务端发送会回复最近一次来包的客户端。

`串口服务器` 模式可同时配置串口参数、TCP/UDP、客户端/服务端和网络地址，并在串口与网络之间双向透明透传。TCP 服务端支持多个客户端；UDP 服务端会将串口数据发给最近一次来包的客户端。

接收区支持将当前日志导出为 UTF-8 文本；发送区支持单次发送和定时发送，时间间隔以毫秒配置，最小为 10 ms。连接断开或发送失败时定时任务会自动停止。

每次底层接收的原始数据都会写入 SQLite：`raw_data` 保存无损 BLOB，`text_data` 保存 UTF-8 字符串，`is_utf8` 标记原数据是否为有效文本。数据库位于 `~/Library/Application Support/GoSerialTool/data`，按日期和 100 MiB 大小滚动分文件保存；监控窗口可直接打开数据库目录。

## Windows 原生桌面版

Windows 64 位安装包位于 `build/windows/GoSerialTool-Windows-x64.zip`。该版本使用 Win32 原生控件，功能包括串口、TCP/UDP 客户端与服务端、串口服务器透明透传、日志导出、定时发送、监控窗口和 SQLite 分文件存储。

重新构建：

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -H windowsgui' -o build/windows/GoSerialTool.exe ./windows
```

重新构建：

```bash
mkdir -p 'build/Go 串口工具.app/Contents/MacOS'
cp desktop/Info.plist 'build/Go 串口工具.app/Contents/Info.plist'
go build -o 'build/Go 串口工具.app/Contents/MacOS/GoSerialTool' ./desktop
codesign --force --deep --sign - 'build/Go 串口工具.app'
```
