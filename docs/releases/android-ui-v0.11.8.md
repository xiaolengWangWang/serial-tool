# CommBox Android 界面正式版 v0.11.8

这是单独提供的 Android APK，方便查看和发送中文串口数据。

- 收发记录与发送输入默认采用文本模式，仍可切换 HEX。
- 中文编码可在 UTF-8 和 GBK 之间切换，同时用于接收显示和发送。
- 发送框改为全宽多行输入，手机布局给发送区域更多空间。
- 弹出输入法时界面随窗口缩放，发送按钮保持可见。
- 使用 CommBox 项目图标作为 Android 启动图标。

APK 包名为 `com.xiaoleng.commbox`，版本号 `0.11.8`，版本代码 `142`，使用与 `v0.11.7` 相同的发布签名。此版只有 Android APK；桌面正式版仍是 `v0.11.7`。

## 设备范围

- 小米 Android／HyperOS 手机：使用此 APK；需要 Android 8.0（API 26）或更新系统。USB OTG、蓝牙权限和安装外部 APK 的操作由设备系统控制。
- 华为 HarmonyOS 4 及更早的 Android APK 环境：使用同一 APK。USB、SPP、BLE 仍要按具体机型和外设实测。
- HarmonyOS 5／NEXT 原生环境：需另行开发并签名 HAP，**此 APK 不适用**。不能把 APK 安装成功视为原生鸿蒙兼容。华为的[应用格式说明](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/gameservice-gameplayer-huawei)分别列出 HarmonyOS 5.0 及以上的 HAP 和 HarmonyOS 4 及以下的 APK。

本地与 [GitHub CI](https://github.com/xiaolengWangWang/serial-tool/actions/runs/38922280822) 均已通过 JVM 单元测试、debug APK 与测试 APK 构建、签名 release APK 构建，并核对签名、包名和版本。下方 SHA256 对应 CI 发布的 APK。当前未连接 Android 设备，尚未完成小米及鸿蒙 4 设备的安装、界面和 USB／蓝牙实测，也没有 HarmonyOS 5／NEXT 的 HAP。

## SHA256

```text
8deedde8877ba49c1a1580c86dfafb4f8f4811c466392493d06f81f780aa0842  CommBox-0.11.8-Android.apk
```
