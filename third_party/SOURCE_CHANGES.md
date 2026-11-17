# 固定源码改动索引

来源与原始 SHA-256 见 [UPSTREAM_FILES.json](UPSTREAM_FILES.json)。该哈希始终表示固定上游版本，用于与当前文件直接比对。

## 修改的上游文件

- `third_party/quic-go/go.mod`
- `third_party/quic-go/packet_unpacker.go`
- `third_party/quic-go/stateless_reset.go`
- `third_party/quic-go/send_queue.go`
- `third_party/quic-go/crypto_stream.go`
- `third_party/quic-go/server.go`
- `third_party/quic-go/packet_packer.go`
- `third_party/quic-go/connection.go`
- `third_party/quic-go/internal/ackhandler/sent_packet_handler.go`
- `third_party/quic-go/http3/server.go`
- `third_party/quic-go/internal/protocol/version.go`
- `third_party/hysteria/core/go.mod`
- `third_party/hysteria/core/server/config.go`
- `third_party/hysteria/core/server/server.go`
- `third_party/hysteria/core/client/config.go`
- `third_party/hysteria/core/client/client.go`

## 新增适配与扩展

- `quic-go/internal/handshake/plaintext_setup.go`：明文握手及上游事件接口。
- `quic-go/internal/handshake/plaintext_protection.go`：直接复制包字节、原包号恢复与字面 token。
- `quic-go/diagnostics.go`、`quic-go/internal/ackhandler/diagnostics.go`：可选被动状态快照；connection/send_queue 中加入默认关闭的采样、时间戳和计数 hook；sent_packet_handler 中加入默认关闭的判丢、误判与 PTO 重新排队计数。计数不依赖 Brutal 等外部控制器更新内置 CUBIC 的公共统计。
- `hysteria/core/client/plaintext_extension.go`：开放原生连接供反向双向流使用。
- `hysteria/core/extension/protocol.go`：复用原编码、分片及拥塞配置校验。
- 项目 `internal/proxy/reverse_*.go`：反向端口注册、原生流和 DATAGRAM 分派。
- 项目 `internal/proxy/udp_*.go`：有界队列、共享预算、到期堆、Linux 批量 IO 和通用回退。
- `socks5/`：固定版本的原始协议文件和许可；不引入未使用的独立服务器、go-cache 或 runnergroup。

## 未复制的密码学实现

上游 internal/handshake 中的 AEAD、cipher suite、header protector、HKDF、TLS/uTLS、TLS tickets 和 key-update 实现，以及 internal/qtls 均未引入本地 fork。保留的 interface/token-generator 由明文适配实现。

`mtu_discoverer.go`、flowcontrol 文件、除 sent_packet_handler.go 外的原 ACK/loss 文件、HY2 Brutal/pacer/frag/proxy 协议文件和 SOCKS5/固定转发文件保持原始字节。`connection.go`、`send_queue.go` 与 `sent_packet_handler.go` 新增被动诊断 hook，保留原发送、恢复和流控算法。

本次未编译或运行验证。
