package vcom

// Version 是 VirtualCOM 独立项目的版本号。
// 与 CommBox 的版本各走各的。
const Version = "0.2.2"

// Backend 标识当前实现路线。用户态方案不安装任何驱动、不需要任何签名,
// 代价是串口参数/控制信号类 API 不可用,详见 README 的兼容性实测表。
const Backend = "usermode-pipe"
