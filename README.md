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

重新构建：

```bash
mkdir -p 'build/Go 串口工具.app/Contents/MacOS'
cp desktop/Info.plist 'build/Go 串口工具.app/Contents/Info.plist'
go build -o 'build/Go 串口工具.app/Contents/MacOS/GoSerialTool' ./desktop
codesign --force --deep --sign - 'build/Go 串口工具.app'
```
