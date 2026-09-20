# CommBox GUI 点击测试记录

测试日期：2026-09-20。版本：v0.8.5 本地优化版，产物为 `build/CommBox.exe`。环境：Windows x64、100% 缩放。GUI 自动化使用真实 Win32 控件及消息，TCP 和 HTTP 收发使用本机测试服务器；测试数据保存在独立的 `build/gui-review/profile` 中。

## 1. 点击与布局

35 项点击回归全部通过，原始结果保存在 `build/gui-review/click-results.json`。

| 范围 | 实际验证 | 结果 |
| --- | --- | --- |
| 发送校验 | 未连接反馈、空报文、非法 HEX、有效 HEX 字节数 | 通过 |
| 选择操作 | 未选中禁用分析、全选、反选、计数与按钮状态 | 通过 |
| 窗口布局 | 默认 1280×820、最小 1024×620、最大化、窄窗口 AI 展开不撑大窗口、连接栏恢复 | 通过 |
| 工作模式 | 串口、TCP、UDP、串口服务器、HTTP 切换 | 通过 |
| TCP | 连接、发送、回显、断开、恢复配置输入 | 通过 |
| 筛选 | RX 筛选、重置、统计刷新时下拉框持续展开 | 通过 |
| 自动发送 | 三次循环按实际收到的字节数核验、定时发送可停止 | 通过 |
| 分析 | 选中报文本地分析、宽屏三栏 | 通过 |
| HTTP | URL 保持原样、文本发送、真实请求响应、退出恢复 HEX | 通过 |
| 工具窗口 | 实时监控、HTTP 工作台、校验与转换、连接管理、历史数据分析、AI 设置 | 通过 |

最终布局另行截图复核，AI 快捷按钮与最近连接不再撑出水平滚动条；“校验与转换”连续关闭、重开两次通过。

本地截图：[默认窗口](../build/gui-review/after-default.png)、[最小窗口](../build/gui-review/after-min.png)、[窄窗口 AI](../build/gui-review/after-ai-min.png)、[最大化 AI](../build/gui-review/after-ai-max.png)。这些截图在忽略的 `build/` 目录中，不随源码提交。

## 2. 托盘与主界面

用户反馈右下角托盘和主界面都未显示。检查时进程仍在，主窗口已最小化；旧代码的应用图标加载返回空，托盘初始化又忽略了图标和显示错误。

修复为从 EXE 资源读取应用图标，并在资源不可用时提供系统图标。启动显示主界面；托盘恢复操作显式还原最小化窗口并请求前台显示；初始化失败写入日志。

补充实测通过：Windows `Shell_NotifyIconGetRect` 能找到 CommBox 图标；模拟托盘左键事件能恢复已最小化主窗口；右键事件能打开“显示主界面 / 退出”菜单；实际点击菜单“退出”能结束测试实例。窗口标题栏也显示 CommBox 图标。测试结束后已打开使用正常用户配置的修复版。

## 3. 用户提供的 cURL

目标为 `GET https://web-dev.iheatingos.com/user-service/system/menu/list`。在 HTTP 工作台导入多行 cURL、核验请求头和 Cookie 保留后发送。

| 项目 | 结果 |
| --- | --- |
| 浏览器多行命令 | LF / CRLF 续行解析通过 |
| 请求头和 Cookie | 导入及生成 cURL 后保留，认证值未写入测试文档 |
| 服务器响应 | HTTP 500，123 bytes，217 ms |
| 业务响应 | `code: 500`，`msg: [防重放请求异常]请勿提交过期请求` |

客户端已成功导入并发出请求，但本次未获得菜单数据。获取菜单需要新的完整 cURL（包含服务端接受的时间与签名）。没有修改签名或伪造时间绕过检查。测试结束后已清空表单中的认证值。

## 4. 自动回归与构建

以下命令通过，包含六项需要桌面的 Win32 控件回归测试：

```powershell
$env:COMMBOX_GUI_TEST = '1'
go test ./... -count=1
go build -ldflags='-H windowsgui -s -w' -o build/CommBox.exe ./windows
```

另新增 cURL 续行与引用内请求体保持原样的单元测试。图标缓存故障用例覆盖工具窗口打开、托盘可见与最小化恢复。

本轮未验证串口硬件通信、远程 AI、更新下载安装及其他 DPI。未发布远程 Release。

## 5. v0.8.6 发版验证

2026-09-20 将上述修复纳入 v0.8.6。重新开启 `COMMBOX_GUI_TEST=1` 执行 `go test ./... -count=1`：101 项通过，2 项跳过，包含的六项真实 Win32 控件回归全部通过。跳过项为缺少 Windows 符号链接权限的子用例，以及默认不启用的联网更新下载测试。

正式产物使用 `go build -trimpath -buildvcs=false -ldflags='-H windowsgui -s -w' -o build/windows-0.8.6/CommBox.exe ./windows` 构建。发布说明与 SHA256 见 [v0.8.6 发布说明](release-v0.8.6.md)。
