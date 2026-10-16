# CommBox HarmonyOS 原生工程

这是为 HarmonyOS 5／NEXT 准备的独立 Stage/HAP 源码工程，不使用 Android APK。当前实现 TCP/UDP 客户端收发、UTF-8／GBK 中文编解码、HEX 输入与显示、多行发送框和 CommBox 图标。网络 API、编解码 API 和 Stage 配置分别参考 [OpenHarmony 5.0 Socket 文档](https://gitee.com/openharmony/docs/blob/OpenHarmony-5.0-Release/zh-cn/application-dev/network/socket-connection.md)、[TextEncoder/TextDecoder 文档](https://gitee.com/openharmony/docs/blob/OpenHarmony-5.0-Release/zh-cn/application-dev/reference/apis-arkts/js-apis-util.md)及[Stage 入门文档](https://gitee.com/openharmony/docs/blob/OpenHarmony-5.0-Release/zh-cn/application-dev/quick-start/start-with-ets-stage.md)。

当前没有 DevEco Studio／HarmonyOS SDK，也没有鸿蒙 5／NEXT 真机，因此**尚未构建 HAP，尚未验证安装与网络收发**。USB 串口、经典蓝牙 SPP、BLE 和 Modbus 界面也尚未移植；不能把这个源码工程或 Android APK 称为完整的 HarmonyOS NEXT 兼容版。

获得官方 SDK 后，在 DevEco Studio 中打开本目录，安装 API 12 对应的 HarmonyOS SDK，同步 `oh-package.json5`，构建 entry HAP，优先修复编译期 API 差异。之后在真机验证 TCP/UDP、中文 GBK、输入法和图标，再逐项移植 USB、SPP、BLE、Modbus。HAP 安装和更新还需要独立的鸿蒙签名配置；Android 的 APK 签名不可复用。

GitHub Actions 的 `Harmony source tests` 工作流只验证可独立运行的 HEX／Modbus 报文逻辑；**它不是 HAP 构建结果**。本地可运行 `node --test apps/harmony/test/packet-codec.test.mjs`。
