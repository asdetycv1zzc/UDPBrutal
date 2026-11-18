# 固定源码改动索引

来源与原始 SHA-256 见 [UPSTREAM_FILES.json](UPSTREAM_FILES.json)。该哈希始终表示固定上游版本，用于与当前文件直接比对。

## 修改的上游文件

- `third_party/hysteria/core/internal/congestion/brutal/brutal.go`：补偿上限为 1.33；新增连接线程内原地更新带宽及补偿开关的方法，保留 ACK 样本、RTT 和 MTU。
- `third_party/hysteria/core/internal/congestion/common/pacer.go`：原算法保留，新增热重载时保留并裁剪已有信用、重设信用时间戳的方法。

- `third_party/quic-go/go.mod`
- `third_party/quic-go/packet_unpacker.go`
- `third_party/quic-go/stateless_reset.go`
- `third_party/quic-go/send_queue.go`
- `third_party/quic-go/crypto_stream.go`
- `third_party/quic-go/server.go`
- `third_party/quic-go/packet_packer.go`
- `third_party/quic-go/connection.go`
- `third_party/quic-go/transport.go`
- `third_party/quic-go/internal/ackhandler/sent_packet_handler.go`
- `third_party/quic-go/internal/ackhandler/interfaces.go`
- `third_party/quic-go/http3/server.go`
- `third_party/quic-go/internal/protocol/version.go`
- `third_party/hysteria/core/go.mod`
- `third_party/hysteria/core/server/config.go`
- `third_party/hysteria/core/server/server.go`
- `third_party/hysteria/core/client/config.go`
- `third_party/hysteria/core/client/client.go`

## 新增适配与扩展

- `internal/config/reload.go`、`internal/proxy/bandwidth.go`：正数固定带宽的配置监视、独立控制流、两端版本号与确认、同连接控制流重试及原地参数更新；CLI 固定初始配置路径，其他字段变更拒绝重载。
- `hysteria/core/extension/bandwidth.go`：能力协商头及连接线程内更新现有 Brutal 的入口；client/server 增加能力协商，server/config 增加新认证读取最新带宽的快照回调。
- `quic-go/connection.go`、`internal/ackhandler/interfaces.go`、`sent_packet_handler.go`：通过连接线程队列更新发送器参数，并重新计算 pacing 定时，不替换发送器或重置恢复状态。

- `quic-go/internal/handshake/plaintext_setup.go`：明文握手及上游事件接口。
- `quic-go/internal/handshake/plaintext_protection.go`：直接复制包字节、原包号恢复与字面 token。
- `quic-go/diagnostics.go`、`quic-go/internal/ackhandler/diagnostics.go`：可选被动状态快照；connection/send_queue 中加入默认关闭的采样、时间戳和计数 hook；sent_packet_handler 中加入默认关闭的判丢、误判与 PTO 重新排队计数。计数不依赖 Brutal 等外部控制器更新内置 CUBIC 的公共统计。
- `quic-go/diagnostics_transport.go`、`diagnostics_socket_*.go`：原始 UDP 入口、连接分派及读取阶段计数；connection 记录初始 Transport，transport 仅加入默认关闭的观测 hook。Linux 按 fd inode 读取 proc 中的 socket 队列/丢弃，诊断调用方共享每秒缓存；其他平台将内核项标为不支持。不修改 socket 选项、报文处理或接收唤醒。
- `quic-go/send_batch*.go`：Linux 非 GSO 路径通过 sendmmsg 提交已有队列中的独立数据报；不等待填满批次，不修改发送许可或 pacer。保留控制消息、数据报边界和部分成功前缀，错误按原逐包路径处理；其他平台回退普通写入。send_queue 接入此可选批量写入，诊断新增调用、接受包数和回退计数。
- `hysteria/core/client/plaintext_extension.go`：开放原生连接供反向双向流使用。
- `hysteria/core/extension/protocol.go`：复用原编码、分片及拥塞配置校验。
- 项目 `internal/proxy/reverse_*.go`：反向端口注册、原生流和 DATAGRAM 分派。
- 项目 `internal/proxy/diagnostics*.go`：在原诊断配置下输出入口/内核计数，并为至少等待 500ms 的写操作自动输出观测摘要；空闲读取不触发。提示不是根因结论，默认关闭。
- 项目 `internal/proxy/udp_*.go`：有界队列、共享预算、到期堆、Linux 批量 IO 和通用回退；Linux 错误时可能返回的负消息数归零，避免回退使用负下标。
- `socks5/`：固定版本的原始协议文件和许可；不引入未使用的独立服务器、go-cache 或 runnergroup。

## 未复制的密码学实现

上游 internal/handshake 中的 AEAD、cipher suite、header protector、HKDF、TLS/uTLS、TLS tickets 和 key-update 实现，以及 internal/qtls 均未引入本地 fork。保留的 interface/token-generator 由明文适配实现。

`mtu_discoverer.go`、flowcontrol 文件、除 sent_packet_handler.go/interfaces.go 外的原 ACK/loss 文件、HY2 frag/proxy 协议文件和 SOCKS5/固定转发文件保持原始字节。Brutal/pacer 增加上述参数更新与信用重设方法；UPSTREAM_FILES.json 继续记录固定上游的原始哈希。`connection.go`、`transport.go` 与 `sent_packet_handler.go` 保留被动诊断 hook；`send_queue.go` 保留批量 IO 和观测。热重载只更新拥塞参数及对应 pacing 时机，不重置流、会话、恢复状态或路径 MTU。

本次未编译或运行验证。
