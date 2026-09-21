# VirtualCOM V1 实现契约

## 1. GUI 与服务 API

包路径 `serial-tool/internal/virtualcom`。API 只使用标准库类型。

```go
type Client interface {
    Snapshot(context.Context) (Snapshot, error)
    Apply(context.Context, Command) (Snapshot, error)
    Diagnostics(context.Context) (string, error)
}
func NewClient() Client

type Snapshot struct {
    UpdatedAt time.Time `json:"updated_at"`
    Driver DriverInfo `json:"driver"`
    Pairs []Pair `json:"pairs"`
    Error string `json:"error,omitempty"`
}
type DriverInfo struct { State, Version, Error string }
type Pair struct {
    ID string
    Enabled bool
    A, B Port
    Error string
}
type Port struct {
    Name, State, Process, Error string
    PID uint32
    TX, RX, Reads, Writes, Errors uint64
    RXUsed, TXUsed, RXCapacity, TXCapacity uint32
    PendingReads, PendingWrites, PendingWaits uint32
    Baud, DataBits, Parity, StopBits uint32
    RTS, CTS, DTR, DSR, DCD, Break bool
}
type Command struct {
    Action, PairID, PortA, PortB string
    Enabled bool
}
```

Driver state 为 `ready/missing/error`。Port state 为 `idle/open/disabled/error/unknown`；UI 负责中文展示。Command.Action 为 `create/rename/delete/enable/restart/purge/reset-stats/set-debug`。空 PortA/PortB 表示自动编号，仅 create 有效。enable/set-debug 使用 Enabled。变更权限与占用检查由服务执行，不能只靠 UI。

管理 IPC：本机命名管道 `\\.\pipe\VirtualCOM.Control.v1`，长度前缀 uint32 LE + UTF-8 JSON（上限 1 MiB）。请求 `{version:1, id:string, method:"snapshot"|"apply"|"diagnostics", command:Command}`；响应 `{version:1,id:string,snapshot:Snapshot,report:string,error:string}`。禁止远程管道客户端；修改操作要求提升的管理员令牌或 SYSTEM。后续可将读权限放宽到交互用户，但不能放宽设备变更权限。

## 2. 驱动设备身份

- Hardware ID：`ROOT\VIRTUALCOM`。
- 每个 COM 口独立、持久化的 PnP 实例；设备参数中保存 `PairId`（UUID 文本）、`Endpoint`（0/1）和 `PortName`（规范 COM 名）。
- 标准接口为 Windows `GUID_DEVINTERFACE_COMPORT`；注册 COM 符号链接与自己的串口枚举项。
- 私有接口 GUID：`{C322112E-CC29-4C24-99A1-0F72D0A50B21}`。
- 私有接口用引用字符串 `backend`；只读观察接口使用同一 GUID、引用字符串 `observer`。服务枚举接口并核对设备参数。
- 设备整体不设置全局独占，正常串口角色只允许一个打开者；后台和观察角色独立计数。不得以所有句柄的 share=0 冲突替代串口角色独占规则。
- 后台 CreateFile 使用 desired access 0、share READ|WRITE、OVERLAPPED、允许身份识别的 SQOS；私有 IOCTL 使用 FILE_ANY_ACCESS，驱动必须额外验证文件角色和请求者身份。`backend` 仅允许 SYSTEM；普通应用只能查询观察接口，不能附加后端、写入接收缓存或修改统计。
- 身份检查使用 UMDF 客户端模拟 / 原始请求凭据，不能检查 WUDFHost 自己的令牌。检查失败默认拒绝。

## 3. 驱动控制协议 v1

共享头文件 `virtualcom/include/virtualcom_ioctl.h` 为数值和布局唯一来源；Go 端按固定字段显式编解码，不用 Go struct 的内存布局直接转换。

DeviceType 为 `FILE_DEVICE_SERIAL_PORT` (0x1b)，METHOD_BUFFERED、FILE_ANY_ACCESS；Function 从 0x800 开始，因此 Query 的值为 0x001b2000。

| Function | 名称 | 输入 | 输出 / 行为 |
| --- | --- | --- | --- |
| 0x800 | QUERY | 无 | `VC_SNAPSHOT`，backend/observer 可用 |
| 0x801 | ATTACH | uint32 version=1 | uint32 version，backend 认证后附加 |
| 0x802 | READ_TX | 无 | 最多 64 KiB 字节；无数据则可取消地等待 |
| 0x803 | WRITE_RX | 最多 64 KiB 字节 | uint32 accepted；空间不足可取消地等待，短接收返回真实字节数 |
| 0x804 | SET_PEER | uint32 signals | bit0 RTS、bit1 DTR、bit2 BREAK、bit3 backendReady；更新本端 CTS/DSR/DCD/BREAK 和事件 |
| 0x805 | WAIT_CHANGE | uint64 lastRevision | uint64 revision；仅在串口打开/关闭、输出信号或参数变化后完成 |
| 0x806 | PURGE | uint32 Windows SERIAL_PURGE_* 标志 | 清理指定队列/缓存，按相应取消语义完成请求 |
| 0x807 | RESET_STATS | 无 | 清零统计，不清理数据、不打断 IO |

QUERY 以外全部要求已授权 backend 文件角色。观察查询不得改变统计或串口设置。

`VC_SNAPSHOT` 为 pack(1)、小端、总计 152 字节：

```c
typedef struct VC_SNAPSHOT {
    uint32_t Size, Version, Flags, OpenPid;
    uint32_t Baud, DataBits, Parity, StopBits;
    uint32_t RTS, CTS, DTR, DSR, DCD, Break;
    uint32_t WaitMask, HoldReasons;
    uint32_t RXUsed, TXUsed, RXCapacity, TXCapacity;
    uint32_t PendingReads, PendingWrites, PendingWaits, LastError;
    uint64_t TXBytes, RXBytes, Reads, Writes, Errors;
    uint64_t Revision, Generation;
} VC_SNAPSHOT;
```

Flags bit0 表示串口打开，bit1 表示后端已附加。TXBytes 表示串口应用被成功接受的写入字节，RXBytes 表示串口应用实际读走的字节。Read/Write 次数按完成的相应应用请求统计；后台 IOCTL 不计为应用收发次数。

每端 RX 和 TX 容量初值均为 256 KiB；每次后端传送最大 64 KiB。实际上限在所有入口校验。WRITE_RX 和 READ_TX 支持并行挂起，状态查询不被数据等待阻塞。

## 4. 服务所有权与持久化

服务名 `VirtualCOM`，正常运行账号 SYSTEM，服务进程只处理固定管理动作与配对字节流。配置位于 `%ProgramData%\VirtualCOM\pairs.json`，日志位于同目录 `logs`，机器级目录仅管理员与 SYSTEM 可写。

管理事务以 UUID 标识配对。只操作自身 hardware ID 且 PairId/Endpoint 与持久化记录匹配的设备。创建失败回滚已建端；删除失败保留异常记录。开启端口由驱动报告，不以枚举进程失败推断为空闲。

一对分别运行 A→B 与 B→A 转发，每个方向最多持有一个 64 KiB 中间块，完整处理短写；RX 满时停止继续抽取该方向 TX。另有输出信号变化监听，传播 RTS/DTR/BREAK。启停/关闭阶段统一取消句柄请求并等待任务退出。

正常通信期间不能丢失字节；服务异常退出会中断当前通信，应有错误状态和诊断，不假装已成功传达。配置和 COM 设备不随 GUI 关闭删除。服务重启重新读取自身设备并建立转发，不重复创建端口。

## 5. 验证边界

纯 Go 测试验证管理和协议，原生驱动逻辑测试验证缓存/超时，真实 COM 测试验证 Windows API。三者在验收记录中分别报告。未安装或未签名的驱动包不能记为已完成真实通信验收。
