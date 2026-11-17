UDPBrutal 已改为直接使用固定版本的 Hysteria2 核心和其 QUIC 连接引擎，保留 ACK/丢包处理、重传、流控、拥塞控制、路径 MTU 探测和 DATAGRAM。原自制可靠 UDP、固定外层 1200 字节上限及 yamux 已移除。

明文握手与包保护替换是独立传输配置，不提供标准 QUIC 或原版 HY2 互通。正向 SOCKS5/TCP/UDP 使用 HY2 核心代码；反向 TCP/UDP 基于原生流和 DATAGRAM 扩展。

源码要求 Go 1.26；构建与发布工作流仍仅手动触发。本次传输重构未编译、构建或进行端到端验证。来源、逐文件哈希和必要改动见 NOTICE.md 与 third_party/UPSTREAM_FILES.json。
