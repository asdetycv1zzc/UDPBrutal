# UDPBrutal 使用指南

[English](README.md) · [项目首页](../README.md)

基于 Hysteria2 **Brutal 拥塞控制**的明文 UDP 代理，提供本地 SOCKS5 上网和服务器到客户端的反向端口映射。正向与反向均支持 **TCP 和 UDP**。

Brutal 基于[指定的 Hysteria 源码](https://github.com/HyNetworks/hysteria/blob/c2b181e0b0e72b077e4b0b7297735af14d1f9929/core/internal/congestion/brutal/brutal.go)，仅将丢包补偿倍率上限从 1.25 提高至 1.33；pacer 与上游字节一致，保留原始 import 路径。完整来源及修改边界见 [NOTICE.md](../NOTICE.md)。客户端和服务器之间不实现或调用数据加密、TLS、证书、QUIC 包保护或混淆算法。`auth` 也是明文字符串。

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

Linux 反向监听使用批量接收，反向 UDP 写入按已有队列内容批量发送；不会等待凑齐一批。批量系统调用不受支持时回退为普通 socket 收发，其他平台保留相同协议和队列行为。反向应用数据报上限仍为原 HY2 的 4096 字节，超过时丢弃，避免转发截断的报文。批量 IO 不改变 pacer、正向代理与 QUIC 丢包恢复机制。

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
| bandwidth.disableLossCompensation | 默认 false，启用最高 1.33 倍的自适应补偿；设为 true 时 ACK rate 固定为 1，不提高发送预算 |
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

Brutal 补偿倍率上限已由固定上游的 **1.25 倍提高至 1.33 倍**。ACK rate 下限为 `1 / 1.33`；样本充足时，补偿倍率为 `1 / max(ACK rate, 1 / 1.33)`。协商速率为 300 mbps 时，最高发送预算为 399 mbps。补偿作用于同一连接的 TCP 流与 UDP DATAGRAM，不会重传丢失的 UDP 载荷或增加 FEC；需要有足够的待发送数据才能用满预算。该行为由本端发送器决定，双向使用新上限需更新两端。关闭补偿时保持 1.0 倍；实际网卡统计仍包含 IP/UDP 头部，也可能出现短时突发。

TLS、ACME 和混淆按明文要求排除。当前 CLI 暴露 SOCKS5、TCP/UDP 转发和反向扩展，尚未暴露 HTTP 代理、TUN、透明代理及 ACL 模式；QUIC 参数和 BBR/Reno 回退已使用原实现。未知配置字段会报错，不能直接复制完整 HY2 配置使用。旧自定义传输的 `transport.udp.idleTimeout` 应改为 `quic.maxIdleTimeout`；原 `handshakeTimeout`、`maxSessions` 删除，由 QUIC 引擎管理握手和流数量。

### 定位反向 TCP 秒级停顿

若 UDP socket 已成功扩大，而吞吐仍出现间歇性停顿，可以在两端 UDPBrutal 进程启用默认关闭的诊断。加入现有 Compose 服务的 `environment`：

```yaml
services:
  udpbrutal:
    environment:
      UDPBRUTAL_DIAGNOSTICS: "200ms"
```

更新源码并重新构建、创建容器后生效：

```bash
docker-compose up -d --build
docker-compose logs -f udpbrutal
```

若使用 Compose v2 插件，将 `docker-compose` 替换为 `docker compose`。二进制直接运行时，可使用 `UDPBRUTAL_DIAGNOSTICS=200ms ./udpbrutal client -c client.yaml`；服务端同样支持。保持 INFO 日志级别。需要重建镜像，已有旧镜像不会因增加环境变量而自动获得此功能。

诊断只做观测，尚不构成对停顿的修复。默认间隔 200ms（设为 true 同样使用 200ms），允许 100ms..60s；删除变量或设为 off 即关闭。每条隧道一份 `transport diagnostics`，每条活动反向 TCP 流另有 `reverse TCP diagnostics`，均带 `role`、`diagnostic_conn` 和流号以区分两端及 iperf 控制/数据连接。

### 自动收包与停顿摘要

保持已有 `UDPBRUTAL_DIAGNOSTICS: "200ms"` 即可。新版本每个采样间隔增加 `socket receive diagnostics`，记录 UDP 入口、连接分派和 socket 内核状态。Linux 自动按 socket inode 读取 `/proc/net/udp`、`udp6`，全进程共享每秒一次的缓存；不需要额外抓包工具、容器权限或修改 sysctl。

反向 TCP 的一次写入等待达到 500ms 时，自动输出一次 `reverse TCP write wait`，包含两层收包计数、内核队列/丢弃、ACK/PTO、发送等待和流控额度。写操作结束并被下一次采样观察到时，输出 `reverse TCP write resumed`；流已关闭时仍可看原来的 `reverse TCP diagnostics closed`。空闲读取不会触发提醒。`hint` 是等待状态提示，不是根因结论；`observed_wait_ms` 的恢复值是采样估计，`counter_window_ms` 表示摘要计数覆盖的观测窗口。

两端更新镜像后，保持原来的传输方式，导出两端完整日志即可，无需手动同步开始、停止抓包。已有配置不用增加新的诊断选项：

```bash
git pull --ff-only
docker-compose up -d --build udpbrutal
```

| 新增字段 | 用途与限制 |
| --- | --- |
| socket_rx_packets / socket_rx_bytes | 本间隔从 UDP socket 包装层读出的数据报数/字节数，在连接 ID 分派之前计数 |
| socket_dispatch_packets | 本间隔完成匹配 handler 调用的数量；用户态接收队列是否接受仍需看 rx_queue_drops |
| socket_unmatched_cid | 本间隔没有匹配连接的短头报文；合法 stateless reset 也可能计入，不能单独认定错误 |
| socket_header_drops / socket_non_quic / socket_empty_packets / socket_read_errors | 连接 ID 解析失败、非 QUIC、空数据报和 socket 读取错误计数 |
| socket_rx_stage / socket_rx_stage_ms / socket_rx_age_ms | 接收线程正在 read、dispatch、read_error 或尚未观测到状态，以及状态持续时间/最后读出报文距今时间；read 等待本身不证明链路丢包 |
| kernel_socket_status / kernel_socket_age_ms | 仅 ok 时解释内核队列/丢弃，年龄表示缓存时效；不支持、proc 不可读或未找到 socket 时不把 0 当作实测值 |
| kernel_rx_drops / kernel_rx_queue / kernel_tx_queue | 该 UDP socket 的累计内核丢弃及内核报告的队列字节；丢弃为 0 不排除网卡、路由和防火墙等其他位置 |
| kernel_rcvbuf / kernel_sndbuf | 开启诊断时读取的实际 socket 缓冲区值，遵循 Linux 的计量方式；读取失败为 0 |

入口与内核指标属于建立连接时的整个 UDP Transport，包含该 socket 上的其他连接；它们不是逐连接统计，也不跟踪后续新增的迁移 socket。计数并非原子事务快照，少量瞬时差异正常。接收线程只更新计数和状态，不读取 proc、不写逐包日志或载荷；内核采样在诊断快照调用方执行。其他平台仍保留入口/分派计数，内核项标为 unsupported_platform。诊断默认关闭，关闭时不扫描 proc 或生成摘要。

| 指标 | 用途与限制 |
| --- | --- |
| quic_tx_mbps / socket_tx_mbps | 引擎发送计数与 socket 接受字节数的速率；普通/GSO 写入成功及 sendmmsg 已接受前缀计入后者，不代表对端已收到 |
| socket_batch_calls / socket_batch_packets | 本采样间隔的批量写入调用数及其已接受的数据报数；用于确认非 GSO 批量路径是否被使用 |
| socket_batch_fallbacks | 本采样间隔批量失败或无进展后转逐包写入的次数；已接受前缀不重复发送 |
| ack_age_ms / packet_age_ms / pto_count | 最近有效 ACK、已处理报文距当前的时间，以及当前 PTO 退避次数；PTO 次数可在 ACK 恢复后归零 |
| inflight / cwnd / send_mode / tracked_packets | 原发送线程采集的在途字节、实际控制器窗口、发送许可及跟踪包数 |
| loop_age_ms / snapshot_ready | 发送线程快照的时效性；快照按自然事件被动采集，空闲时变旧正常，不会唤醒发送线程来取样 |
| loss_timer_ms / pacing_timer_ms | 最近快照里的恢复/节奏定时器距当前的时间；需同时看对应 timer_set 和 loop_age_ms，负值本身不能证明定时器故障 |
| send_queue / socket_write_pending_ms / socket_write_last_ms | 发送队列长度、正在进行的 socket 写入耗时和上次写入耗时 |
| socket_write_calls / socket_write_errors | 普通及批量写入操作的累计调用/错误次数；错误包含随后由逐包回退恢复的批量失败，不等于隧道断开或线路丢包 |
| rx_queue_drops | QUIC 用户态收包队列累计丢包数；它与内核 socket 的 ss d 计数不同 |
| to_tunnel_stage / to_local_stage | 向隧道/向本地连接的拷贝分别正在 read、write、running 或 done；wait_ms 是当前这次调用的耗时 |
| buffered / stream_credit / conn_credit | 流内等待发送的字节和剩余流控额度；仅在 send_ready/conn_credit_ready 为 true 时解释对应值 |
| receive_offset / receive_highest | 已读取位置与最高收到位置；需结合 to_local_stage 和 receive_ready 判断是否存在等待按序交付 |
| sample_ms | 实际采样间隔；异常变长也可能是调度或日志输出受阻，不能单独据此认定 GC 或网络故障 |
| recovery_lost_delta / recovery_lost_bytes_delta | 恢复引擎按包数/时间阈值判丢的包数和字节增量，包含 PMTU 探测包；不是线路物理丢包率 |
| recovery_spurious_delta / pto_requeued_delta | 已判丢后又被 ACK 的包数，以及 PTO 重新排队的包数；两者独立计数，不从前一项相减 |

诊断包含轻量计数、时钟读取和可用时的非阻塞流状态快照，启用后有额外开销。它不输出逐包日志或数据内容，不调用发送唤醒，不修改拥塞或流控决定；关闭时不创建采样器，也不包装反向 TCP 的逐次读写。不要把空闲连接的旧快照当作故障。

恢复计数从首次开启诊断采样时开始，并与 `loop_age_ms` 一起解释。旧版本的 `quic_lost_delta` 来自上游内置 CUBIC 的统计；使用 Brutal 等外部控制器时不会更新，因而恒为 0 也不能证明没有丢包。新版本直接观察恢复事件，不依赖拥塞控制器更新统计。

若活动流仍有待发送数据和流控额度，同时 `inflight` 达到 `cwnd`、ACK 长时间没有进展、`pto_count` 上升，则发送方在等待确认及超时探测。它描述了停顿机制，不能单独确定最初的丢包位置。成功的 socket 写入不保证报文上网或到达对端；`quic_rx_mbps` 在连接分派之后计数，且按两位小数显示，不能替代 socket 收包计数。

两端诊断显示 `gso=true` 时，可先在两端已有的 `quic` 配置中合并以下选项，再重启进程；已有镜像支持此选项，无需为修改配置重建镜像：

```yaml
quic:
  disableGSO: true
```

```bash
docker-compose restart udpbrutal
```

确认新日志为 `gso=false` 后，沿用出现问题时的传输方向、带宽和端口。关闭 GSO 仅用于区分批量发送路径，可能增加 CPU 开销；结果变化本身也不足以证明驱动缺陷。

Linux 在 GSO 关闭且有多个已排队数据报时，自动使用 `sendmmsg` 一次提交至多 8 个独立数据报。它不拼接 UDP 载荷、不设置 `UDP_SEGMENT`，也不等待凑满批次；原拥塞窗口、pacer 和发送许可决定仍由上游引擎作出。控制消息、目的地址和数据报边界保留，批量缓冲与元数据复用。

部分成功时只继续提交未接受的后缀；失败时对该后缀逐包处理，保留原 PMTU 错误语义。不支持批量操作或权限拒绝时，对当前连接禁用批量写入。其他平台、自定义且不支持批量写入的连接仍使用普通路径。反向 UDP 批量 IO 同时将 Linux 错误时可能返回的负消息数归零，确保回退不会使用负下标。

保持 `quic.disableGSO: true` 即可使用这一发送路径，无额外批量配置项。已有旧镜像需更新源码并重建才能获得此优化及 `socket_batch_*` 指标：

```bash
git pull --ff-only
docker-compose up -d --build udpbrutal
```

启用诊断后，`socket_batch_calls` 和 `socket_batch_packets` 显示实际批量提交情况；低负载时只有一个待发包，因此为 0 正常。该改动尚未经过构建或性能验证，不保证特定吞吐，也没有确认此前 GSO 开启时停顿的根因。

如果仍出现停顿，在两端宿主机同时捕获 UDP 头部，并保留对应诊断日志。以下命令使用当前示例隧道端口；如有修改，替换 `443`：

```bash
tcpdump -i any -nn -s 128 -B 4096 -w /tmp/udpbrutal.pcap 'udp port 443'
```

捕获一次停顿后按 Ctrl+C，分别标注服务器和客户端文件，并保留 tcpdump 结束时的捕获丢包统计。抓包用于区分发送端、链路、接收宿主机与连接分派；GSO 和校验和卸载可能影响主机侧抓包的显示，不能把大包或显示的校验和异常直接当成线路错误。

## 传输与验证范围

Brutal 与 pacer 位于 `third_party/hysteria/core/internal/congestion/`。Brutal 仅将最高丢包补偿倍率由 1.25 提高至 1.33；pacer 与固定上游字节一致，原始 import 保留。确认/丢包处理、重传、流与连接流控、拥塞控制接入及 MTU 探测使用 `third_party/quic-go/` 中的原引擎，不再使用自制可靠 UDP、512 包窗口或 yamux。逐文件原始哈希见 [UPSTREAM_FILES.json](../third_party/UPSTREAM_FILES.json)。

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
