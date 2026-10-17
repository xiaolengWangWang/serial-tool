# CommBox v0.11.10 — Windows 与 Android 正式版

本版同时提供 Windows 和 Android 安装包。Android 版延续 0.11.9 的横竖屏布局、中文收发，以及 USB、SPP、BLE 设备选择与帮助说明，并改为正式发布渠道。

## 更新

- Android“检查更新”现在可通过正式版发布信息找到新的 APK；从 0.11.8 或 0.11.9 更新到本版后，包名与签名保持一致，安装仍由 Android 系统确认。
- Windows 版本号和可执行文件版本资源同步升至 0.11.10。Windows 的默认构建与发布范围不变。
- 本版发布 Windows x64 和 Android APK；macOS、Linux 仍使用上一正式版 0.11.7。

## 验证范围

CI 对 Windows 执行 Go 测试并构建完整压缩包，对 Android 执行 JVM 单元测试、测试 APK 编译和签名 APK 构建。Android 设备上的软键盘、横竖屏、USB 和蓝牙操作仍需真机验证。
