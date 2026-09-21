# CommBox v0.9.1

Windows x64，解压后运行 `CommBox.exe`。发布包附带 VirtualCOM v0.2.0 的图形界面和命令行，无需安装虚拟串口驱动。

## 本版变化

本版只改打包，功能与 v0.9.0 完全相同。

- 四个程序内嵌版本信息：右键属性 → 详细信息可以看到版本号、产品名和文件说明，CommBox 显示 0.9.1，VirtualCOM 显示 0.2.0。
- 去掉调试信息瘦身约 25%：CommBox.exe 22.3 → 16.8 MB，CommBox-CLI.exe 12.1 → 8.8 MB，VirtualCOM-GUI.exe 8.1 → 6.1 MB，VirtualCOM.exe 2.9 → 2.1 MB，压缩包 25.8 → 13.1 MB。符号表保留，崩溃栈仍带函数名。
- CommBox-CLI.exe 补上应用图标；单独下载的 exe 附件改名为带版本号的 `CommBox-0.9.1.exe`。

## 使用

先运行 `VirtualCOM-GUI.exe` 创建一对端口并保持运行，再打开两个 CommBox 窗口，分别连接两端。命令行使用 `CommBox-CLI.exe -list` 查看端口、`-port COM号` 打开。

## 验证与边界

- 全量 Go 与 Windows GUI 回归测试通过；发布产物逐项校验 PE 头、构建来源 commit、版本资源和压缩包内容。
- VirtualCOM 适配的边界与 v0.9.0 相同：只提供字节流端口，波特率、数据位、校验、停止位和控制信号不生效，未适配的第三方串口软件仍不兼容。
- VirtualCOM 必须保持运行并与 CommBox 位于同一用户登录会话；退出后端口会移除。Windows 10 和长时间运行尚未验收。
- 程序未做商业代码签名。
