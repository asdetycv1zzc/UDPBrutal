# UDPBrutal

基于 Hysteria2 **Brutal 拥塞控制**的明文 UDP 代理，提供本地 SOCKS5 上网和服务器到客户端的反向端口映射。正向与反向均支持 **TCP 和 UDP**。

Brutal 和 pacer 直接取自 [指定的 Hysteria 源码](https://github.com/HyNetworks/hysteria/blob/c2b181e0b0e72b077e4b0b7297735af14d1f9929/core/internal/congestion/brutal/brutal.go)，仅替换 import 路径；完整来源及修改边界见 [NOTICE.md](NOTICE.md)。客户端和服务器之间不实现或调用数据加密、TLS、证书、QUIC 包保护或混淆算法。`auth` 也是明文字符串。

这是独立明文协议，**不能与 Hysteria2 客户端或服务器互通**。Hysteria2 的 QUIC/TLS 承载被可靠 UDP 和 yamux 流复用替代，命令、配置及安装布局参考 Hysteria2。反向端口映射是沿用其双向流转发思路实现的项目扩展。

## 功能

| 模式 | 监听位置 | 目标连接位置 | 协议 |
| --- | --- | --- | --- |
| socks5 | 客户端 | 服务器访问目标网络；域名在服务器解析 | CONNECT / UDP ASSOCIATE |
| tcpForwarding / udpForwarding | 客户端 | 服务器访问固定目标 | TCP / UDP |
| reverseTCP / reverseUDP | 服务器 | 客户端访问本地或内网服务 | TCP / UDP |

TCP 支持半关闭；UDP 保留数据报边界，按访问来源分配独立会话和目标 UDP socket，默认空闲 60 秒回收。多个本地代理连接共享同一条隧道。隧道断开后客户端自动重连并重新注册反向监听；已有 TCP 连接不跨重连恢复。

## 从源码安装

需要 Go 1.23 或更新版本。当前仓库为私有仓库时，克隆或下载需使用有仓库权限的 GitHub 账户。

```bash
git clone https://github.com/asdetycv1zzc/UDPBrutal.git
cd UDPBrutal
go build -trimpath -o udpbrutal ./cmd/udpbrutal
```

代码使用 Go 标准网络接口，兼顾 Linux、macOS、Windows，以及 amd64/arm64；systemd 安装脚本仅适用于 Linux。Windows 从源码构建时可指定 `-o udpbrutal.exe`。Linux/macOS 也可以使用 `make build`、`make install`。

当前交付源码和配置，不在本轮生成跨平台发行包。仓库的 Build 工作流**仅允许手动触发**，提交代码或创建标签不会触发构建。后续需要时可在 Actions 选择操作系统、架构；默认仅上传构建产物，只有显式填写已有 `release_tag` 才发布对应 Release。尚未发布 Release 时使用源码安装。

Linux 上沿用 Hysteria2 的“单文件二进制 + /etc 配置 + systemd”安装形式：

```bash
sudo bash scripts/install_server.sh --local ./udpbrutal
sudo nano /etc/udpbrutal/config.yaml
sudo systemctl enable --now udpbrutal-server
journalctl -u udpbrutal-server -f
```

安装脚本保留已有配置，不自动启动服务。不传 `--local` 时会在源码目录编译；已有 Release 时也支持 `--version vX.Y.Z` 下载相应归档。脚本应从完整源码目录运行，私有仓库的 Release 建议先自行下载再使用 `--local`。

## SOCKS5 正向上网

服务器 `server.yaml`：

```yaml
listen: :443
auth:
  type: password
  password: example-password
bandwidth:
  up: 100 mbps
  down: 100 mbps
```

客户端 `client.yaml`：

```yaml
server: your-server.example.com:443
auth: example-password
bandwidth:
  up: 100 mbps
  down: 100 mbps
socks5:
  listen: 127.0.0.1:1080
  disableUDP: false
```

分别启动：

```bash
# 服务器
./udpbrutal server -c server.yaml
# 客户端
./udpbrutal client -c client.yaml
```

将浏览器或应用的 SOCKS5 代理设为 `127.0.0.1:1080`。使用域名代理解析时，目标域名经隧道发送到服务器。SOCKS5 可配置 `username`、`password`。支持 IPv4、IPv6、域名目标；不支持 SOCKS BIND 或 SOCKS UDP 分片。

## TCP + UDP 反向映射

服务器配置中加入：

```yaml
reverse:
  enabled: true
  maxListeners: 16
```

客户端配置中加入：

```yaml
reverseTCP:
  - listen: 0.0.0.0:18080
    remote: 127.0.0.1:8080
reverseUDP:
  - listen: 0.0.0.0:15353
    remote: 127.0.0.1:5353
    timeout: 60s
```

访问服务器的 TCP `18080` 会转发到客户端的 TCP `127.0.0.1:8080`；发往服务器 UDP `15353` 的数据报会转发到客户端 UDP `127.0.0.1:5353`，响应返回原发送方。TCP 和 UDP 可以使用相同端口号。`listen` 是服务器地址，`remote` 是客户端地址，二者不可互换。只做反向映射时可以省略 `socks5`。

所有反向监听在握手中一起注册；任一端口绑定失败则撤销本次注册。端口由对应隧道持有，隧道关闭后释放。多客户端不能同时占用同一地址、端口和协议。使用 `:0` 可申请临时端口，实际地址会写入日志。

网络需允许服务器的隧道 UDP 端口和需要暴露的 TCP/UDP 映射端口。反向流量由客户端已建立的隧道承载，客户端不需要公网入站端口。

## 其他配置与命令

完整示例见 [examples/client.yaml](examples/client.yaml)、[examples/client-reverse.yaml](examples/client-reverse.yaml)、[examples/server.yaml](examples/server.yaml)。

```bash
./udpbrutal -c client.yaml                         # 默认 client
./udpbrutal server -c server.yaml -l debug -f json
./udpbrutal server -c server.yaml --check          # 仅解析和检查配置
./udpbrutal version
```

| 配置 / 参数 | 行为 |
| --- | --- |
| -c / --config | 指定 YAML 或 JSON；未指定时依次查找当前目录、~/.udpbrutal/、/etc/udpbrutal/ 中的 config.yaml、config.yml、config.json |
| -l / --log-level；-f / --log-format | debug/info/warn/error；console/json。环境变量 UDPBRUTAL_LOG_LEVEL、UDPBRUTAL_LOG_FORMAT |
| --disable-update-check | 为类似 Hysteria 的服务启动命令保留；本程序没有自动更新检查 |
| bandwidth.up / down | 十进制 bps/kbps/mbps/gbps/tbps；up 为本端发送速率，down 为本端允许的对端发送速率；两端取较小值。up 省略时为 100 mbps，down 省略时不额外限制 |
| bandwidth.disableLossCompensation | 原 Brutal 开关；设为 true 时 ACK rate 固定为 1 |
| transport.type | 仅 udp；省略也使用 udp |
| transport.udp.idleTimeout | 隧道超时，默认 30s，最小 5s；每 2s keepalive |
| transport.udp.handshakeTimeout | UDP 握手超时，默认 5s |
| transport.udp.maxSessions | 服务器会话上限，默认 128 |
| udpIdleTimeout | 服务器正向 UDP relay 空闲超时，默认 60s |
| udpForwarding[].timeout / reverseUDP[].timeout | 对应 UDP 来源会话空闲超时，默认 60s，最大 24h |
| disableUDP | 禁用服务器正向 UDP relay；reverseUDP 单独由 reverse.enabled 控制 |
| HYSTERIA_BRUTAL_DEBUG / UDPBRUTAL_BRUTAL_DEBUG | 设为 true 输出原 Brutal 调试信息 |

`tls`、`acme`、`obfs`、`quic`、HTTP 代理、TUN、透明代理、ACL、BBR 等 Hysteria2 配置不在实现范围内；未知配置字段会报错，不能直接复制一份完整 Hysteria2 配置使用。

## 传输与验证范围

Brutal 使用上游的 5 个一秒采样槽、50 个最小样本、0.8 ACK rate 下限、2 倍带宽时延积拥塞窗口，以及相同的 token bucket pacer。实际 UDP 包上限 1200 字节，数据包和重传包都经过 Brutal。可靠 UDP 采用选择确认、去重、重排、RTT/RTO 估计、指数退避及 512 包接收窗口；各发送/接收缓冲均有界。

UDP 应用数据也通过可靠流承载，因此隧道丢包会引起重传和共享队头阻塞；它与 Hysteria2 的 QUIC DATAGRAM 延迟和丢包特性不同。未实现 MTU 探测、NAT 地址迁移、拥塞自适应算法或 Hysteria2 线协议兼容。

按项目要求，此版本不新增或运行功能测试，当前不执行跨平台构建。已完成的本机编译和静态审查不替代端到端验证；真实代理流量、丢包网络及多来源运行行为尚未验证。

## Docker

```bash
docker build -t udpbrutal .
docker run --rm --network host \
  -v "$PWD/server.yaml:/etc/udpbrutal/config.yaml:ro" \
  udpbrutal
```

上例使用 Linux host 网络，让反向 TCP 和 UDP 监听地址直接对应宿主机；采用 bridge 网络时，需分别发布隧道端口和所有映射端口，并区分 /tcp、/udp。

## 许可

[MIT](LICENSE)，含 Hysteria 上游版权声明；第三方依赖见 [NOTICE.md](NOTICE.md)。
