package vcom

// Version 是 VirtualCOM 独立项目的版本号。
// 与 CommBox 的版本各走各的:VirtualCOM 并入 CommBox 时,CommBox 升到 0.9.0。
const Version = "0.2.1"

// Backend 标识当前实现路线。用户态方案不安装任何驱动、不需要任何签名,
// 代价是串口参数/控制信号类 API 不可用,详见 README 的兼容性实测表。
const Backend = "usermode-pipe"
