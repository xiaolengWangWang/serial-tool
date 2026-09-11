---
name: commbox-ai-analyze
description: 用 DeepSeek 对串口/TCP/UDP 通信报文(HEX)做协议识别与异常诊断。当用户给出 HEX 报文、粘贴抓包数据、或要求分析 CommBox 采集的通信内容时使用。仅用系统自带命令行(curl/jq/sqlite3)调用 DeepSeek，分析全部由 DeepSeek 完成，无需 Python 或任何编程语言环境。
---

# CommBox AI 报文分析

把 CommBox 桌面版的 DeepSeek 报文诊断能力独立成 skill。给定 HEX 报文和传输类型，
用一条 curl 命令把数据交给 DeepSeek，由 DeepSeek 返回协议解析、异常与现场排查建议。
不受桌面版“必须有实时会话报文”的限制，任意 HEX 均可分析。

## 何时使用
- 用户贴出 HEX 报文并问“这是什么协议 / 有没有异常 / 帮我分析”。
- 用户让你分析 CommBox 抓到的串口 / TCP / UDP 数据。
- 需要对 Modbus、自定义帧等做逐字节解读和排查建议。

## 运行方式（仅系统自带命令行，无 Python）

把 `TRANSPORT`（`串口`/`TCP`/`UDP`）和 `HEX`（可多帧、每行一帧、可带方向标注）填好后执行：

```bash
DB="$HOME/Library/Application Support/CommBox/data/commbox-settings.sqlite3"
KEY="${DEEPSEEK_API_KEY:-$(sqlite3 "$DB" "SELECT value FROM settings WHERE key='deepseek.api_key'")}"
BASE="${DEEPSEEK_BASE_URL:-$(sqlite3 "$DB" "SELECT value FROM settings WHERE key='deepseek.base_url'")}"; BASE="${BASE:-https://api.deepseek.com}"
MODEL="${DEEPSEEK_MODEL:-$(sqlite3 "$DB" "SELECT value FROM settings WHERE key='deepseek.model'")}"; MODEL="${MODEL:-deepseek-chat}"

TRANSPORT="串口"
HEX="接收 01 03 02 00 64 B9 AF
发送 01 03 00 00 00 01 84 0A"

[ -n "$KEY" ] || { echo "未找到 DeepSeek API Key：设 DEEPSEEK_API_KEY，或在 CommBox「AI 增强分析设置」里配置"; exit 1; }

jq -nc --arg model "$MODEL" \
       --arg sys "你是工业通信现场诊断助手。按以下步骤分析：1) 协议识别（Modbus RTU/TCP、DL/T645、CJ/T188、自定义帧等）并说明依据；2) 逐字节解析（用表格列出字段/值/含义）；3) 校验核对（CRC/校验和/LRC 的算法与字节序，能核对则核对）；4) 异常与风险（异常响应、长度不符、CRC 错误、半包/粘包、可疑值）；5) 现场排查建议。只依据给定数据，未提供的参数（波特率、数据位、校验、寄存器表等）不臆测、标注“需确认”；区分确定结论与推测；结论仅作排查建议，以设备协议文档为准。" \
       --arg usr "请分析以下 ${TRANSPORT} 通信报文。报文为 HEX：
${HEX}" \
  '{model:$model,temperature:0.1,messages:[{role:"system",content:$sys},{role:"user",content:$usr}]}' \
| curl -s -m 60 "${BASE%/}/chat/completions" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $KEY" -d @- \
| jq -r '.choices[0].message.content // ("错误：" + (.error.message // "无返回"))'
```

## Key / 配置来源（按优先级）
1. 环境变量 `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` / `DEEPSEEK_MODEL`。
2. CommBox 本地设置库 `~/Library/Application Support/CommBox/data/commbox-settings.sqlite3`
   的 `deepseek.api_key` / `deepseek.base_url` / `deepseek.model`（与桌面版“AI 增强分析设置”共用同一份配置）。

Base URL 默认 `https://api.deepseek.com`，模型默认 `deepseek-chat`。

> 提示词与桌面版 CommBox 内嵌的分析指南（`desktop/ai_analysis_guide.md`）同源，保证终端与 GUI 的分析框架一致。

## 注意
- 只发送 HEX 报文与传输类型，不发送 IP、设备名或主机名。
- 单次输入建议不超过 16KB；超出请先筛选。
- 只读取 CommBox 设置库，不修改任何本地数据。
- 结论仅作现场排查建议，以设备协议文档为准。
