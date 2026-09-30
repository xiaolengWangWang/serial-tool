# CommBox v0.9.17

本版提供 Windows x64、macOS Apple Silicon / Intel 安装包和 Linux amd64 命令行程序。Windows 对话页新增附件功能；macOS 和 Linux 的功能与上一版本一致，版本号同步到 0.9.17。

## 本版变化

- Windows AI 对话可添加日志、TXT、CSV、JSON、Markdown、XML、YAML、DOCX 和可选中文字的 PDF；选中的内容随下一条问题发送。可在发送前移除附件。扫描版 PDF 和旧版 DOC 暂不支持。
- 可添加 PNG、JPEG、GIF 和 WebP 图片，发送给支持视觉输入的模型；附件在点击发送前不会上传。
- 每次最多 6 个附件。PDF / DOCX 上限 5 MiB，图片上限 2 MiB；文本提取和对话总量设有上限。PDF 在独立进程中解析，限制内存与处理时间。

## 验证与边界

- macOS 上全量 Go 测试、短模式竞态检测、原生桌面构建通过；AppKit 布局自检检查 484 个界面组合，0 个失败。
- Windows x64 交叉构建及静态检查通过。此构建环境没有 Windows 桌面，Windows 附件选择和视觉模型交互未做原生界面验收。
- 版本资源、安装包内容和 SHA256 校验值以发布产物为准。程序未做商业代码签名；macOS 使用临时签名。
