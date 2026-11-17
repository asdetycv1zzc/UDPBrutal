# UDPBrutal 使用指南

[English](README.md) · [项目首页](../README.md)

基于 Hysteria2 **Brutal 拥塞控制**的明文 UDP 代理，提供本地 SOCKS5 上网和服务器到客户端的反向端口映射。正向与反向均支持 **TCP 和 UDP**。

Brutal 和 pacer 与 [指定的 Hysteria 源码](https://github.com/HyNetworks/hysteria/blob/c2b181e0b0e72b077e4b0b7297735af14d1f9929/core/internal/congestion/brutal/brutal.go)字节一致，包含原始 import 路径；完整来源及修改边界见 [NOTICE.md](../NOTICE.md)。客户端和服务器之间不实现或调用数据加密、TLS、证书、QUIC 包保护或混淆算法。`auth` 也是明文字符串。

当前直接使用固定版本的 Hysteria2 核心与 QUIC 引擎，保留原生流、DATAGRAM、确认/丢包处理、重传、流控和路径 MTU 探测，仅替换为明文握手和无包保护配置。**不能与未经修改的 Hysteria2 客户端或服务器互通**。反向端口映射是基于原生双向流和 DATAGRAM 的项目扩展。

## 功能

| 模式 | 监听位置 | 目标连接位置 | 协议 |
| --- | --- | --- | --- |
| socks5 | 客户端 | 服务器访问目标网络；域名在服务器解析 | CONNECT / UDP ASSOCIATE |
| tcpForwarding / udpForwarding | 客户端 | 服务器访问固定目标 | TCP / UDP |
| reverseTCP / reverseUDP | 服务器 | 客户端访问本地或内网服务 | TCP / UDP |

TCP 使用原生双向流；UDP 保留数据报边界，按访问来源分配独立会话和目标 UDP socket，默认空闲 60 秒回收。多个本地代理连接共享同一条隧道。隧道断开后客户端自动重连并重新注册反向监听；已有 TCP 连接不跨重连恢复。此次传输协议也与之前的 UB01 原型不兼容，升级时需同时更新 UDPBrutal 的客户端和服务器。

## 从源码安装

需要 Go 1.26 或更新版本。当前仓库为私有仓库时，克隆或下载需使用有仓库权限的 GitHub 账户。

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

### 依赖下载超时

如果构建提示连接 `proxy.golang.org` 的 `i/o timeout`，可为当前命令选择能访问的 Go 模块代理，例如：

```bash
GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' go build -trimpath -o udpbrutal ./cmd/udpbrutal
```

这里的 `|` 表示遇到超时等任何错误时尝试下一来源；默认配置里的逗号只在 HTTP 404/410 时回退。也可以替换为自己的模块代理。无需删除 `go.sum`、清空模块缓存或关闭校验。

使用源码安装脚本时，将变量传入 sudo 后的环境：

```bash
sudo env GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' bash scripts/install_server.sh
```

SOCKS5 所需的原始协议编解码已随源码保存，HY2 的 SOCKS5 服务实现保持原样；没有引入未使用的 SOCKS5 独立服务器，因此不再依赖 `go-cache` 或 `runnergroup`。其他 Go 模块及旧版本 Go 自动下载的新工具链仍可能需要联网。已有完整模块缓存时可用 `GOPROXY=off` 禁止模块下载。代理回退规则见 [Go 官方模块文档](https://go.dev/ref/mod#environment-variables)。

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

### 反向 UDP 性能与资源配置

客户端和服务器都支持以下本地配置，不参与线路协议协商：

```yaml
reverseUDPOptions:
  maxSessions: 4096
  queuePackets: 64
  maxQueuedBytes: 16777216
  batchSize: 32
```

| 字段 | 含义 |
| --- | --- |
| maxSessions | 服务器每条反向隧道、或客户端实例的活动 UDP 来源会话上限，默认 4096 |
| queuePackets | 客户端每个会话、服务器每个 UDP 映射的队列包数上限，默认 64 |
| maxQueuedBytes | 上述实例范围内共享的排队字节预算，默认 16 MiB；不含 socket、重组状态、正在处理的数据和其他对象 |
| batchSize | Linux 每批最多处理的包数，默认 32，范围 1..64；设为 1 使用逐包路径，其他平台使用通用收发 |

队列满或达到字节预算时丢弃新到的数据报；达到会话上限时不创建新会话。不要通过无限扩大队列掩盖拥塞，否则会增加积压时延和内存占用。

发送路径复用序列化缓冲和固定地址；客户端会话表分区，服务器按映射持锁。回包通过有界队列交给独立 UDP 写入协程，统一 DATAGRAM 接收入口不等待目标 socket。过期回收使用单调时钟、到期堆和共享定时器，不再每秒遍历全部会话或为每个客户端会话维护定时器。

Linux 反向监听使用批量接收，反向 UDP 写入按已有队列内容批量发送；不会等待凑齐一批。批量系统调用不受支持时回退为普通 socket 收发，其他平台保留相同协议和队列行为。反向应用数据报上限仍为原 HY2 的 4096 字节，超过时丢弃，避免转发截断的报文。Brutal、pacer、正向代理与 QUIC 丢包恢复机制保持原始实现。

## 其他配置与命令

完整示例见 [examples/client.yaml](../examples/client.yaml)、[examples/client-reverse.yaml](../examples/client-reverse.yaml)、[examples/server.yaml](../examples/server.yaml)。

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
| bandwidth.up / down | 十进制 bps/kbps/mbps/gbps/tbps；up 为本端发送速率，down 为本端允许的对端发送速率；两端取较小值。未提供速率时按原 HY2 带宽协商及 BBR/Reno 回退逻辑处理，不再自行填充 100 mbps |
| bandwidth.disableLossCompensation | 原 Brutal 开关；设为 true 时 ACK rate 固定为 1 |
| transport.type | 仅 udp；省略也使用 udp |
| quic.maxIdleTimeout | 原 HY2 QUIC 空闲超时，默认 30s，范围 4s..120s |
| quic.keepAlivePeriod | 客户端 keepalive，默认 10s，范围 2s..60s |
| quic.maxIncomingStreams | 原生接收流数上限；服务器默认 1024，客户端配置反向 TCP 时也显式启用 |
| quic.disablePathMTUDiscovery | 默认 false；使用上游路径 MTU 探测与平台支持判断 |
| quic.initStreamReceiveWindow / maxStreamReceiveWindow | 原生流接收窗口，默认 8MB |
| quic.initConnReceiveWindow / maxConnReceiveWindow | 原生连接接收窗口，默认 20MB |
| congestion.type / bbrProfile | 交给上游的 BBR/Reno 选择与 BBR profile 校验；已知带宽时按原逻辑使用 Brutal |
| udpIdleTimeout | 服务器正向 UDP relay 空闲超时，默认 60s，范围 2s..600s |
| udpForwarding[].timeout / reverseUDP[].timeout | 对应 UDP 来源会话空闲超时，默认 60s，最大 24h |
| disableUDP | 禁用服务器正向 UDP relay；reverseUDP 单独由 reverse.enabled 控制 |
| HYSTERIA_BRUTAL_DEBUG / UDPBRUTAL_BRUTAL_DEBUG | 设为 true 输出原 Brutal 调试信息 |

TLS、ACME 和混淆按明文要求排除。当前 CLI 暴露 SOCKS5、TCP/UDP 转发和反向扩展，尚未暴露 HTTP 代理、TUN、透明代理及 ACL 模式；QUIC 参数和 BBR/Reno 回退已使用原实现。未知配置字段会报错，不能直接复制完整 HY2 配置使用。旧自定义传输的 `transport.udp.idleTimeout` 应改为 `quic.maxIdleTimeout`；原 `handshakeTimeout`、`maxSessions` 删除，由 QUIC 引擎管理握手和流数量。

## 传输与验证范围

Brutal 与 pacer 位于 `third_party/hysteria/core/internal/congestion/`，与固定上游字节一致，包括原始 import。确认/丢包处理、重传、流与连接流控、拥塞控制接入及 MTU 探测使用 `third_party/quic-go/` 中的原引擎，不再使用自制可靠 UDP、512 包窗口或 yamux。逐文件原始哈希见 [UPSTREAM_FILES.json](../third_party/UPSTREAM_FILES.json)。

UDP 恢复为原生不可靠 DATAGRAM，并使用 HY2 原始 UDPMessage、分片/重组和会话管理。外层 UDP 包不再固定封顶 1200 字节，使用上游初始包大小、EMSGSIZE 回退、对端限制和路径 MTU 探测；上游 `InitialPacketSize=1280`、QUIC 的 1200 字节最低要求、HY2 的 `MaxDatagramFrameSize=1200` 是不同概念。原 HY2 应用 UDP 缓冲 `MaxUDPSize=4096` 保留。TLS 相关握手、包保护、票据、0-RTT 密钥和指纹处理移除；无加密配置和反向扩展的必要差异见 [PROTOCOL.md](../PROTOCOL.md)。

本次传输重构未进行编译、容器构建、跨平台构建或运行测试。仅进行了源码来源比对、语法/格式、依赖导入和静态审阅，这些不构成运行正确性证明。真实代理流量、丢包、重连和跨平台行为尚未验证。

## Docker Compose

需要安装 Docker 和 Compose v2，并使用 Linux 容器。仓库提供的 [docker-compose.yml](../docker-compose.yml) 通过已有 Dockerfile 启动 UDPBrutal **服务器**，使用默认桥接网络。

先在仓库根目录准备配置：

```bash
cp examples/server.yaml config.yaml
```

编辑 `config.yaml`，让服务器的 `auth.password` 与客户端的 `auth` 一致。使用默认端口映射时保留 `listen: :443`；示例配置已启用 TCP 和 UDP 反向监听。

准备好后构建镜像并启动：

```bash
docker compose up -d --build
docker compose logs -f udpbrutal
```

首次启动会为 Docker 引擎的本机平台构建镜像，无需预先发布的镜像。宿主机的 `config.yaml` 以只读方式挂载到容器的 `/etc/udpbrutal/config.yaml`；启动前必须先创建该文件。

Docker 构建下载 Go 依赖超时时，可传入模块代理，Compose 会将它转交给 Dockerfile 的构建参数：

```bash
GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' docker compose up -d --build
```

单独构建镜像时使用 `docker build --build-arg 'GOPROXY=https://goproxy.cn|https://proxy.golang.org|direct' -t udpbrutal .`。镜像基础层下载使用 Docker 自己的网络设置，不由 GOPROXY 控制。

| 宿主机端口 | 容器端口 | 用途 |
| --- | --- | --- |
| 443/udp | 443/udp | 客户端连接服务器的隧道 |
| 18080/tcp | 18080/tcp | 示例 TCP 反向监听 |
| 15353/udp | 15353/udp | 示例 UDP 反向监听 |

客户端仍按前面的 SOCKS5 或反向映射示例单独运行。`server` 填写 Docker 宿主机的地址和对外发布的隧道 UDP 端口。反向监听由客户端注册，仅在 Docker 中发布端口不会自动创建反向映射。

示例客户端中的 `reverseTCP[].listen` 保持 `0.0.0.0:18080`，`reverseUDP[].listen` 保持 `0.0.0.0:15353`，它们表示容器内的监听地址和端口。新增反向端口时，需在 Compose 的 `ports` 中追加对应的 `/tcp` 或 `/udp` 映射；仅修改宿主机端口时，容器内的监听端口不变。

使用 SOCKS5 或固定正向转发时，目标地址从容器网络访问，其中 `127.0.0.1` 表示容器本身。

常用管理命令：

```bash
# 查看服务状态
docker compose ps
# 修改 config.yaml 后重新创建容器
docker compose up -d --force-recreate
# 更新源码后重新构建并启动
docker compose up -d --build
# 停止并移除容器，宿主机上的 config.yaml 会保留
docker compose down
```

## 许可

[MIT](../LICENSE)，含 Hysteria 上游版权声明；第三方依赖见 [NOTICE.md](../NOTICE.md)。
