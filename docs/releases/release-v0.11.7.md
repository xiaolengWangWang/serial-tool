# CommBox v0.11.7 — Android 首版与连接稳定性修复

本版新增原生 Android 应用，并修复桌面端异步断连事件可能混入报文回调的问题。

## Android

- 支持 TCP 客户端/服务端、UDP 收发、USB 串口、蓝牙 SPP 和 BLE UART；可发送 HEX 或文本报文，并保留本地历史、导出 CSV。
- 提供 Modbus RTU/TCP 03、04、06、10 指令模板。安装包更新会校验 SHA256、应用包名和签名，并交由 Android 系统确认安装。
- 修复 AGP 9 构建配置、SDK 37.0 平台选择及 Kotlin 导入错误；JVM 单元测试、debug APK 和测试 APK 已在本机完成构建。USB、蓝牙和安装升级仍需真实设备验证。

## 桌面端

- 会话切换或断连后，旧连接的异步回调不再被当作当前报文处理。
- 保持 v0.11.6 的数据格式与现有连接操作方式。

## 下载

- [GitHub 发行版](https://github.com/xiaolengWangWang/serial-tool/releases/tag/v0.11.7)提供 Windows、macOS、Linux 和 Android 安装包及 SHA256 校验值。
- [Gitee 发行版](https://gitee.com/sun_xuanqi/serial-tool/releases/tag/v0.11.7)同步发布后可供国内下载。
