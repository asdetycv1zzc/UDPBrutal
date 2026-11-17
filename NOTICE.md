# 来源与许可

UDPBrutal 以 MIT 许可发布，保留 Hysteria 的原始 MIT 版权声明，见 LICENSE。

## Hysteria2

固定参考版本：[`HyNetworks/hysteria@c2b181e0b0e72b077e4b0b7297735af14d1f9929`](https://github.com/HyNetworks/hysteria/tree/c2b181e0b0e72b077e4b0b7297735af14d1f9929)。

| UDPBrutal 文件 / 行为 | 上游来源 | 处理方式 |
| --- | --- | --- |
| internal/congestion/brutal/brutal.go | core/internal/congestion/brutal/brutal.go，blob ec61bf1735ef80fdea7ccc9b5601f25e298deb77 | 仅替换三个 import 路径，算法原样保留 |
| internal/congestion/common/pacer.go | core/internal/congestion/common/pacer.go，blob e0bddbe2e75b56203be4e7f8e4725e15f5e3e180 | 仅替换两个 import 路径 |
| SOCKS5 协商与 TCP/UDP 分派 | app/internal/socks5/server.go | 按相同握手、命令和转发流程实现，改用本项目明文隧道 |
| TCP 双向复制 | app/internal/forwarding/tcp.go；core/server/copy.go | 参考双向 io.Copy，补充 TCP 半关闭 |
| 命令与配置形式 | app/cmd/root.go；app/cmd/client_test.yaml；app/cmd/server_test.yaml | 保留 client/server、-c、日志参数及相关 YAML 字段，配置范围见 README |
| 目标地址与 UDP 会话语义 | PROTOCOL.md | 保留 host:port 目标及会话隔离思路，使用独立明文封装 |

传输适配常量还参考 Hysteria 固定依赖的 [`HyNetworks/quic-go@2895f039137a`](https://github.com/HyNetworks/quic-go/tree/2895f039137a)：`InitialPacketSize=1280`、`MinPacingDelay=1ms`。UDPBrutal 随后调用 `SetMaxDatagramSize(1200)`。未复制或依赖 QUIC、TLS、包保护或加密代码。

反向 TCP/UDP 注册消息、端口所有权、UDP 来源到会话的映射及可靠 UDP 传输层是本项目扩展；不将它们表述为 Hysteria2 原有线上协议。其注册、转发及回收行为见 PROTOCOL.md。

## 运行时依赖

- `github.com/hashicorp/yamux v0.1.2`：MPL-2.0；仅使用流复用，无加密；[源码及许可](https://github.com/hashicorp/yamux/tree/v0.1.2)。未修改其源文件。
- `gopkg.in/yaml.v3 v3.0.1`：MIT / Apache-2.0；解析配置；[源码及许可](https://github.com/go-yaml/yaml/tree/v3.0.1)。未修改其源文件。

发行归档包含本声明；第三方源代码可通过上述固定版本及 go.mod/go.sum 获取。
