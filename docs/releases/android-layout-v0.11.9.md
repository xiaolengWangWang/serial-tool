# CommBox Android 横竖屏界面正式版 v0.11.9

这是单独的 Android APK 界面优化正式版。桌面正式版仍为 v0.11.7；后续默认发版仍只面向 Windows。

- 手机横屏和竖屏使用“连接／收发”切换页，连接后自动进入收发页；平板保持双栏。
- 键盘弹出或横屏高度不足时，发送区可滚动，输入框和发送按钮仍可操作。
- USB、SPP、BLE 设备改为整行展示；USB 设备 ID 自动由所选设备填入，三个入口均有“？”说明。
- BLE UUID 收入高级设置，默认保留 Nordic UART Service；TCP、SPP、BLE 地址分别保存，避免切换连接方式时混用。
- 保留中文 UTF-8／GBK 收发、HEX、Modbus 预置指令及 CommBox 图标。

包名 `com.xiaoleng.commbox`，版本号 `0.11.9`，版本代码 `150`，使用与上一版相同的发布签名。Android 8.0 及更新系统可安装；小米 Android／HyperOS 和能安装 APK 的 HarmonyOS 4 设备仍需真机验证 USB、SPP、BLE。HarmonyOS 5／NEXT 原生设备需要 HAP，不能安装此 APK。

本地已通过 JVM 单元测试及 debug、签名 release APK 构建。[GitHub CI](https://github.com/xiaolengWangWang/serial-tool/actions/runs/38991685932) 也已通过单元测试、debug／测试 APK 编译及签名 release APK 构建，并核对签名、包名和版本。下方 SHA256 对应 CI 发布的 APK。目前没有连接 Android 设备，横竖屏、软键盘、USB 与蓝牙操作尚未完成真机测试。

## SHA256

```text
18bd606be03dcd00046584b67e276a48483ad0fd967a101b14038cfc9f4edd84  CommBox-0.11.9-Android.apk
```
