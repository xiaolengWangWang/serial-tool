# CommBox v0.8.1 — Windows 修复版

本次提供 Windows x64 桌面程序，解压后运行 CommBox.exe，无需安装 Go。

## 修复

- 修复 Windows 数据库分析路径转为 SQLite 文件 URI 时的兼容问题。
- 为 macOS 桌面代码及其测试添加平台约束，Unix PTY 测试仅在非 Windows 平台运行。
- 将符号链接检查拆成独立子测试，普通 Windows 账户缺少创建链接权限时明确跳过，其他数据库检查继续执行。

## 验证

- Windows 下 go test -count=1 -timeout 90s ./...、go vet ./... 和桌面构建通过。
- 符号链接子测试已在管理员权限下单独执行通过。
- 桌面程序启动响应正常，标题显示 CommBox v0.8.1 - Windows。

## 已知限制

- Windows 虚拟串口尚不可作为已验证功能使用：此前测试出现 CNCA1 打不开及 com0com 驱动管理命令等待超时，本版未修复这些问题。需要虚拟串口的用户请暂缓采用此功能。
- 本次未构建或验证 macOS/Linux 程序；源码中的平台约束不代表已完成这些平台的测试。
- Windows 程序未做商业代码签名。
