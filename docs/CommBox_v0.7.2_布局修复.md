# CommBox v0.7.2 布局修复

## 修复内容

- 数据选择按钮改为原生 NSStackView 操作行，解决“反选”和“分析选中数据”控件相交；详情与统计各占独立区域。
- 发送格式、间隔、历史、收藏和发送按钮使用布局约束，消除 NSTabView 内容边距和不同缩放规则造成的重叠。
- 使用一致的初始布局尺寸构建控件，再挂到窗口；左侧连接参数保持顶部对齐且内部坐标稳定，放大后切换模式不会与连接状态/历史交叉。
- HTTP 模式只显示一个 URL 标签。
- 分析区域使用纵向布局，统计明确分行，结果区域随窗口高度扩展。

## 验证

`desktop/layout_check.m` 检查真实 AppKit 控件的 alignment rect，相交或越出容器时返回非零退出码。

```sh
go build -o /tmp/commbox-layout-check ./desktop
check_dir=$(mktemp -d /tmp/commbox-layout-check-XXXXXX)
COMMBOX_LAYOUT_CHECK_DIR="$check_dir" /tmp/commbox-layout-check
```

检查尺寸为 1280×700、1600×900、1920×1080、恢复 1280×700；每次尺寸变化后依次切换五种工作模式和两个发送页签。测试不建立设备连接或调用 AI，输出控件树的 PNG 预览。系统合成的页签装饰在离屏缓存图中可能呈黑色，不能代替完整屏幕截图验收。

修复前该检查复现 110 次相交；修复后 40 个布局组合中未检测到相交/越界。该检查不验证报文解析或 AI 诊断正确性。

Go 测试、macOS amd64/arm64 和 Linux amd64 构建均通过。尚未发布 GitHub Release。
