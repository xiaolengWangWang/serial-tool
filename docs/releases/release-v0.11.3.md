# CommBox v0.11.3 — 软件内更新与 Gitee 下载源

本版让 Windows 版在软件里直接完成更新，并新增 Gitee 下载源，国内检查和下载更新不再受 GitHub 连接速度影响。

## 软件内更新

- “帮助 → 检查更新”同时查询 Gitee 和 GitHub，取最新的发布；一样新时优先从 Gitee 下载，出错时自动换另一边。
- Windows 解压运行的 CommBox.exe 点击“下载并安装”：下载后按 SHA256 校验，替换程序目录里的文件，点击“立即重启”生效，也可以下次启动再生效。数据目录和设置不受影响。
- 按已安装的包选择更新包：GUI 包只更新主程序和手册，完整包同时更新命令行和 VirtualCOM 程序。
- 程序目录不可写入或文件被占用时，恢复原文件并说明原因，可改用发布页手动下载。
- 更新对话框显示安装包来自哪个下载源。macOS 同样优先从 Gitee 下载 DMG，安装方式不变。

## 下载

- 国内推荐 Gitee：https://gitee.com/sun_xuanqi/serial-tool/releases ，安装包和校验值与 GitHub 相同。
- 日常 Windows 桌面使用可选 GUI.zip 或 GUI.7z；需要命令行或独立 VirtualCOM 时选完整 ZIP / 7z。
- macOS 提供 Apple Silicon / Intel DMG；Linux 提供 amd64 / arm64 程序及含手册的压缩包。

## 注意

v0.11.2 及更早版本只从 GitHub 检查更新，升级到本版仍走原来的下载流程（下载后关闭 CommBox 解压覆盖）；从本版起，之后的更新都可以在软件内完成。
