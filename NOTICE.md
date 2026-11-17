# 来源、改动与许可

UDPBrutal 的目标是直接保留 Hysteria2 的代理与 QUIC 传输机制，替换为明文握手和无包保护的传输配置，并扩展反向 TCP/UDP 映射。当前版本不再使用自制可靠 UDP 或 yamux。

## 固定上游版本

- [HyNetworks/hysteria](https://github.com/HyNetworks/hysteria/tree/c2b181e0b0e72b077e4b0b7297735af14d1f9929)：`c2b181e0b0e72b077e4b0b7297735af14d1f9929`。
- [HyNetworks/quic-go](https://github.com/HyNetworks/quic-go/tree/2895f039137a405a34cffbcb27510ad31717d89a)：`2895f039137a405a34cffbcb27510ad31717d89a`，即该 Hysteria 版本固定依赖的提交。

运行时代码直接保存在 third_party/，没有复制测试、mock、证书样例或原加密算法实现。完整逐文件来源与原始 SHA-256 见 [UPSTREAM_FILES.json](third_party/UPSTREAM_FILES.json)，改动文件索引见 [SOURCE_CHANGES.md](third_party/SOURCE_CHANGES.md)。

| 项目文件 | 上游来源 | 处理方式 |
| --- | --- | --- |
| third_party/hysteria/core/internal/congestion/brutal/brutal.go | core/internal/congestion/brutal/brutal.go | 字节一致，包括原始 import |
| third_party/hysteria/core/internal/congestion/common/pacer.go | core/internal/congestion/common/pacer.go | 字节一致 |
| third_party/hysteria/core/internal/protocol/ | core/internal/protocol/ | HTTP/3 认证头、TCP/UDP 消息编码原样保留 |
| third_party/hysteria/core/internal/frag/ | core/internal/frag/ | 原分片与重组代码 |
| third_party/hysteria/core/client/、server/ | core/client/、server/ | 保留正向代理、会话管理、带宽协商；加入反向流/DATAGRAM 分派 hook，移除证书要求及 TLS 指纹 |
| internal/socks5/server.go | app/internal/socks5/server.go | 字节一致 |
| internal/forwarding/ | app/internal/forwarding/ | TCP/UDP 固定转发代码字节一致 |
| third_party/quic-go/connection.go、internal/ackhandler/、internal/flowcontrol/、mtu_discoverer.go | 同名上游文件 | 字节一致；保留原连接状态机、可靠性、流控与路径 MTU 探测 |
| third_party/quic-go/internal/handshake/plaintext_*.go | 新增适配 | 明文交换原 transport parameters；包内容和头部直接复制，不派生密钥或生成密码学标签 |
| third_party/quic-go/packet_packer.go、packet_unpacker.go、server.go | 同名上游文件 | 封包和解析跳过加密包头采样；适配无标签的小 ACK/PING 和错误响应包 |
| third_party/quic-go/crypto_stream.go | 同名上游文件 | 关闭依赖 TLS ClientHello 的切分/混淆 |
| third_party/quic-go/internal/protocol/version.go | 同名上游文件 | 使用独立版本标识，避免宣称标准 QUIC 互通 |
| third_party/quic-go/stateless_reset.go | 同名上游文件 | 重置标识改为字面连接 ID，不使用 HMAC |
| third_party/quic-go/http3/server.go | 同名上游文件 | 移除 TLS ticket 初始化、加解密回调和证书监听入口 |
| third_party/hysteria/core/extension/、internal/proxy/reverse_*.go | 新增扩展 | 通过原生流和 DATAGRAM 实现反向 TCP/UDP 注册和转发 |
| go.mod / third_party/**/go.mod | 固定源码模块清单 | 本地 replace、Go 1.26 及实际运行时依赖 |

## 明文配置边界

TLS 握手、AES/ChaCha 等数据和头部加密、密钥派生、证书处理、TLS 票据、0-RTT 密钥、密钥更新和 TLS 指纹不属于此配置。fork 中保留 `tls.Config` 等类型以匹配上游 HTTP/3 API，但不创建 TLS 连接。

ACK、丢包处理、重传、流和连接流控、拥塞控制接入、PMTU 探测、HTTP/3/QPACK 消息、正向代理消息及 UDP 分片均使用固定上游源码。无加密与反向映射必需的差异明确列于 [PROTOCOL.md](PROTOCOL.md)。这不是标准 QUIC，也不能与原版 HY2 建立连接。

## 许可

- Hysteria 核心与应用代码：MIT，保留 [上游版权声明](third_party/hysteria/core/LICENSE.md)。
- quic-go：MIT，保留 [上游许可](third_party/quic-go/LICENSE)。
- SOCKS5 库、YAML 及其他模块：按其各自固定版本许可使用，版本与校验和见 go.mod / go.sum。
- 项目根目录许可见 [LICENSE](LICENSE)。

本次重构未执行编译、容器构建、跨平台构建或功能测试；源码来源可核对不等于运行行为已验证。
