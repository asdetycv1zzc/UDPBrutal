# Getting Started

[中文文档](README.zh.md) · [Project Home](../README.md)

UDPBrutal provides a local SOCKS5 proxy, fixed TCP/UDP forwarding, and reverse TCP/UDP port mapping. Client and server use the pinned Hysteria2 core and QUIC engine, with a replacement plaintext handshake and unprotected packets. TCP uses native streams; UDP uses native unreliable DATAGRAMs and the original Hysteria fragmenter.

## Installation

Go 1.26 or newer is required to build from source. Access to a private repository requires an authorized GitHub account.

```bash
git clone https://github.com/asdetycv1zzc/UDPBrutal.git
cd UDPBrutal
go build -trimpath -o udpbrutal ./cmd/udpbrutal
```

On Windows, use `-o udpbrutal.exe`. The Go source considers Linux, macOS, Windows, amd64, and arm64. The systemd installer is Linux-specific.

Builds are only triggered manually from GitHub Actions. Select a target operating system and architecture when a binary is needed. An existing `release_tag` must be supplied explicitly to publish a release; otherwise the workflow only uploads an artifact. Pushing a commit or creating a tag does not start a build.

For Linux installation with a binary you have already built:

```bash
sudo bash scripts/install_server.sh --local ./udpbrutal
sudo nano /etc/udpbrutal/config.yaml
sudo systemctl enable --now udpbrutal-server
journalctl -u udpbrutal-server -f
```

The installer preserves an existing configuration and leaves starting the service to you. Run it from a complete source checkout.

### Module download timeouts

For an `i/o timeout` reaching `proxy.golang.org`, select a reachable Go module proxy for that command, for example:

```bash
GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' go build -trimpath -o udpbrutal ./cmd/udpbrutal
```

Replace the first URL with your own module proxy if appropriate. A pipe (`|`) retries the next source after any error, including a timeout; a comma retries only after HTTP 404/410. Keep `go.sum` and checksum verification enabled. See the [Go module reference](https://go.dev/ref/mod#environment-variables).

To pass this setting through the source installer:

```bash
sudo env GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' bash scripts/install_server.sh
```

The original SOCKS5 protocol codecs are included locally, with the HY2 SOCKS5 adapter unchanged. The unused standalone SOCKS5 server is excluded, removing the `go-cache` and `runnergroup` dependencies. Other modules and automatic Go toolchain downloads may still require network access. With a complete module cache, `GOPROXY=off` disables module downloads.

## SOCKS5 Proxy

Server configuration, `server.yaml`:

```yaml
listen: :443
auth:
  type: password
  password: example-password
bandwidth:
  up: 100 mbps
  down: 100 mbps
```

Client configuration, `client.yaml`:

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

Start the server and client on their respective machines:

```bash
# Server
./udpbrutal server -c server.yaml
# Client
./udpbrutal client -c client.yaml
```

Point an application's SOCKS5 settings at `127.0.0.1:1080`. SOCKS5 supports CONNECT and UDP ASSOCIATE, IPv4, IPv6, domain targets, and optional `username`/`password` authentication. Domain targets sent through SOCKS5 are resolved on the server. SOCKS BIND and SOCKS UDP fragmentation are unsupported.

## Reverse TCP and UDP Mapping

Enable reverse listeners in the server configuration:

```yaml
reverse:
  enabled: true
  maxListeners: 16
```

Add mappings to the client configuration:

```yaml
reverseTCP:
  - listen: 0.0.0.0:18080
    remote: 127.0.0.1:8080
reverseUDP:
  - listen: 0.0.0.0:15353
    remote: 127.0.0.1:5353
    timeout: 60s
```

TCP connections to server port `18080` are forwarded to the client's local TCP port `8080`. Datagrams sent to server UDP port `15353` reach the client's local UDP port `5353`; replies return to the original sender.

`listen` belongs to the server and `remote` belongs to the client. A reverse-only client can omit `socks5`. TCP and UDP may use the same port number.

Each UDP source gets an independent session and connected target socket. Its packets use native unreliable DATAGRAMs. Idle associations expire after the configured timeout. Reverse listeners are registered together; a bind failure rolls back that registration. Listeners are released when their tunnel closes and registered again after reconnection. Existing TCP streams do not survive a reconnect.

Allow the server's tunnel UDP port and the TCP/UDP mapping ports through the surrounding network. The client initiates the tunnel and needs no public inbound port.

### Reverse UDP performance and resource options

Client and server accept the same local settings; these are not negotiated wire fields:

```yaml
reverseUDPOptions:
  maxSessions: 4096
  queuePackets: 64
  maxQueuedBytes: 16777216
  batchSize: 32
```

| Setting | Meaning |
| --- | --- |
| maxSessions | Active source-session limit per registered server peer, or per client instance; default 4096 |
| queuePackets | Packets per client session queue or server mapping queue; default 64 |
| maxQueuedBytes | Shared queued-byte budget for that scope; default 16 MiB, excluding sockets, reassembly state, packets being processed and other objects |
| batchSize | Maximum Linux batch size; default 32, range 1..64. Use 1 for ordinary socket IO. Other platforms use the portable path |

New datagrams are dropped when a queue or byte budget is full. New sessions are not created at the session limit. Increasing queues can increase memory use and queueing latency.

Sending reuses serialization buffers, prefixes and fixed address strings. Client session maps are sharded and server routing locks belong to individual mappings. Bounded queues keep local socket writes out of the connection-wide DATAGRAM receive loop. Idle expiry uses a monotonic deadline heap and one shared timer, replacing full-map scans and per-client-session timers.

Linux reverse listeners receive in batches, and reverse UDP writers send batches of already queued packets without waiting to fill a batch. Unsupported batch syscalls fall back to ordinary socket IO. The original 4096-byte application UDP limit remains; larger reverse datagrams are dropped instead of forwarding a truncated packet. Brutal, pacing, the forward proxy and native QUIC loss recovery remain unchanged.

## Fixed Forwarding

To listen locally and reach fixed targets through the server:

```yaml
tcpForwarding:
  - listen: 127.0.0.1:8088
    remote: internal.example.com:80
udpForwarding:
  - listen: 127.0.0.1:5353
    remote: 1.1.1.1:53
    timeout: 60s
```

## Configuration and Commands

Complete examples are available for the [server](../examples/server.yaml), [client](../examples/client.yaml), and [reverse-only client](../examples/client-reverse.yaml). The [Chinese guide](README.zh.md#其他配置与命令) also includes a configuration reference.

```bash
./udpbrutal -c client.yaml
./udpbrutal server -c server.yaml -l debug -f json
./udpbrutal server -c server.yaml --check
./udpbrutal version
```

Client mode is the default. Without `-c`, configuration is loaded from `config.yaml`, `config.yml`, or `config.json` in the current directory, then `~/.udpbrutal/`, then `/etc/udpbrutal/`.

`bandwidth.up` sets the local sending rate; `bandwidth.down` participates in the original peer-rate negotiation. Units are decimal bps/kbps/mbps/gbps/tbps. Omitted rates retain the upstream automatic-control behavior; a 100 mbps default is not injected. Set `bandwidth.disableLossCompensation: true` to keep Brutal's ACK rate at 1 when Brutal is selected.

The source defaults are a 30-second QUIC idle timeout and 10-second client keepalive. Forwarded UDP associations default to a 60-second idle timeout. Configuration parsing rejects unknown fields.

### QUIC and congestion settings

Use Hysteria-style `quic` settings, for example:

```yaml
quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520
  maxIdleTimeout: 30s
  keepAlivePeriod: 10s
  disablePathMTUDiscovery: false
```

`maxIncomingStreams` configures the native stream limit. For reverse TCP the
client explicitly enables server-initiated bidirectional streams (default 1024
when mappings are configured), because upstream HTTP/3 otherwise disables them.

Known bandwidth selects the original Brutal path. When bandwidth is omitted or
the server chooses automatic control, the source BBR/Reno selection applies;
`congestion.type` and `congestion.bbrProfile` pass through the upstream validators.

The old `transport.udp.idleTimeout` setting moves to `quic.maxIdleTimeout`.
The former custom-transport `handshakeTimeout` and `maxSessions` settings are
removed; the original QUIC engine now owns handshake timing and stream limits.

There is no fixed 1200-byte outer UDP ceiling. The source's 1280-byte initial
packet setting, fallback and MTU probing are distinct from the original
1200-byte DATAGRAM-frame limit. The original 4096-byte application UDP buffers
remain in the Hysteria core and SOCKS/UDP-forwarding adapters.

## Compatibility

The command names, configuration layout, and Linux installation layout follow Hysteria2 conventions. UDPBrutal uses an independent plaintext transport and requires UDPBrutal at both ends.

This revision also changes the earlier UB01 prototype's wire format. Upgrade both UDPBrutal endpoints together; the old custom transport and the new plaintext QUIC profile do not interoperate.

TLS, ACME and obfuscation are excluded by design. The CLI currently exposes SOCKS5, TCP/UDP forwarding and the reverse extension; HTTP proxy, TUN, transparent proxy and ACL modes are not exposed. The original QUIC settings and BBR/Reno fallback are retained. A complete Hysteria2 configuration cannot be used directly.

The original native stream and DATAGRAM implementations now carry traffic. UDP datagrams are not retransmitted through a shared reliable byte stream. QUIC stream/connection flow control, ACK and loss processing, and path MTU discovery are pinned upstream code. The source Hysteria core's path-manager setting is preserved; TLS-specific resumption and fingerprinting are unavailable.

See [PROTOCOL.md](../PROTOCOL.md) for wire behavior and [NOTICE.md](../NOTICE.md) for the pinned source references and adaptation boundaries.

### Diagnosing reverse TCP stalls

When throughput still pauses after the UDP socket buffers have been raised, enable the optional diagnostics on both UDPBrutal endpoints. Merge this into the existing Compose service's environment:

```yaml
services:
  udpbrutal:
    environment:
      UDPBRUTAL_DIAGNOSTICS: "200ms"
```

Update the source and rebuild/recreate the containers:

```bash
docker-compose up -d --build
docker-compose logs -f udpbrutal
```

For the Compose v2 plugin use `docker compose`. A directly launched binary accepts `UDPBRUTAL_DIAGNOSTICS=200ms ./udpbrutal client -c client.yaml`, and server mode accepts the same variable. Keep INFO logging enabled. Adding the variable to an old image does not install the new diagnostic code.

These observers help locate the stall; they are not a stall fix. Diagnostics are disabled by default. Use true for 200ms, a duration from 100ms to 60s, or off to disable them. A tunnel emits transport diagnostics; active reverse TCP relays also emit reverse TCP diagnostics. Role, diagnostic_conn and stream distinguish endpoints and iperf control/data streams.

| Fields | Interpretation |
| --- | --- |
| quic_tx_mbps / socket_tx_mbps | Engine send rate and complete successful socket-write rate; successful GSO writes count the full batch and do not prove peer delivery |
| ack_age_ms / packet_age_ms / pto_count | Age of the last valid ACK and processed packet, and current PTO backoff count; ACK progress can reset the PTO count |
| inflight / cwnd / send_mode / tracked_packets | Owner-loop snapshot of in-flight bytes, the actual controller's window, send permission and tracked packets |
| loop_age_ms / snapshot_ready | Snapshot freshness. The observer does not wake the connection loop, so stale snapshots on idle connections are expected |
| loss_timer_ms / pacing_timer_ms | Last sampled timer deadline relative to now; interpret with timer_set and loop_age_ms, not as an independent proof of a timer fault |
| send_queue / socket_write_pending_ms / socket_write_last_ms | Socket-send queue length and current/last socket-write duration |
| rx_queue_drops | User-space QUIC receive-queue drops, distinct from the kernel socket's ss d counter |
| to_tunnel_stage / to_local_stage | Whether each copy direction is reading, writing, running or done; wait_ms measures that current IO operation |
| buffered / stream_credit / conn_credit | Pending stream data and remaining send credit; only interpret values whose corresponding Ready flag is true |
| receive_offset / receive_highest | Consumed offset and highest received offset, interpreted with receive_ready and the relay IO stage |
| sample_ms | Actual report interval; scheduling and blocked log output can both delay the reporter |
| recovery_lost_delta / recovery_lost_bytes_delta | Packets and bytes declared lost by recovery's packet/time thresholds, including PMTU probes; not a physical path-loss rate |
| recovery_spurious_delta / pto_requeued_delta | Previously declared-lost packets subsequently ACKed, and packets requeued for PTO; independent counters, not subtracted from the preceding fields |

The enabled observer adds counters, clock reads and nonblocking stream snapshots. It emits no per-packet logs or payloads and does not change congestion/flow-control decisions or schedule sends. With diagnostics disabled, there is no reporter or per-IO relay wrapper.

Recovery counters start at the first enabled diagnostic snapshot and must be interpreted with `loop_age_ms`. Earlier versions used `quic_lost_delta` from upstream's built-in CUBIC statistics. External controllers such as Brutal do not update that counter, so a constant zero does not establish zero loss. The current fields observe recovery events independently of the congestion controller.

If an active stream has pending data and send credit, while `inflight` reaches `cwnd`, ACK progress stops and `pto_count` rises, the sender is waiting for acknowledgments and timeout probes. This identifies the pause mechanism, not the original drop location. Successful socket writes do not prove wire delivery. `quic_rx_mbps` is counted after connection dispatch and rounded to two decimal places; it is not a raw socket receive counter.

When both endpoints report `gso=true`, merge this option into each endpoint's existing `quic` configuration and restart the processes. Existing images already support this option, so changing the configuration requires no image rebuild:

```yaml
quic:
  disableGSO: true
```

```bash
docker-compose restart udpbrutal
```

Confirm `gso=false` in the new logs, keeping the original transfer direction, bandwidth and ports. Disabling GSO isolates the batched send path and may increase CPU use; a changed result alone does not establish a driver defect.

If pauses persist, capture UDP headers simultaneously on both hosts and retain matching diagnostic logs. Replace `443` if the tunnel uses another port:

```bash
tcpdump -i any -nn -s 128 -B 4096 -w /tmp/udpbrutal.pcap 'udp port 443'
```

After one pause, stop with Ctrl+C, label the server/client files, and retain tcpdump's capture-drop summary. Captures distinguish sender, path, receiving host and connection dispatch. GSO and checksum offload can affect host-side captures; an oversized packet or displayed checksum mismatch alone is not evidence of a wire error.

## Validation Scope

This rewritten transport has not been compiled, container-built or run. Source hashes, syntax/formatting, dependency imports and static review can be checked against the pinned upstream revision; these do not establish runtime correctness. Proxy traffic, losses, reconnection and cross-platform behavior remain unverified. No functional test suite was added or run, and builds still require a manual request.

## Docker Compose

Use Docker with Compose v2 and Linux containers. The included [docker-compose.yml](../docker-compose.yml) starts the UDPBrutal **server** using the repository's Dockerfile and the default bridge network.

From the repository root, prepare the configuration:

```bash
cp examples/server.yaml config.yaml
```

Edit `config.yaml`: set the server `auth.password` to the same value as the client's `auth`, and keep `listen: :443` with the default port mapping. The example enables reverse TCP and UDP listeners.

Build and start the server when you are ready:

```bash
docker compose up -d --build
docker compose logs -f udpbrutal
```

The first command builds an image for the Docker engine's native platform. No prebuilt image is required. The host's `config.yaml` is mounted read-only at `/etc/udpbrutal/config.yaml`; prepare it before starting Compose.

If module downloads time out during the Docker build, Compose forwards this setting as a build argument:

```bash
GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct' docker compose up -d --build
```

For a standalone build, use `docker build --build-arg 'GOPROXY=https://goproxy.cn|https://proxy.golang.org|direct' -t udpbrutal .`. Docker base-image downloads use Docker's own network configuration, independently of GOPROXY.

| Host port | Container port | Purpose |
| --- | --- | --- |
| 443/udp | 443/udp | Client-to-server tunnel |
| 18080/tcp | 18080/tcp | Example reverse TCP listener |
| 15353/udp | 15353/udp | Example reverse UDP listener |

Run the client separately using the [SOCKS5](#socks5-proxy) or [reverse mapping](#reverse-tcp-and-udp-mapping) configuration. Its `server` address uses the Docker host's address and published UDP tunnel port. Reverse mappings are still registered by the client; publishing a Docker port alone does not create a reverse listener.

For the example client mappings, keep `reverseTCP[].listen: 0.0.0.0:18080` and `reverseUDP[].listen: 0.0.0.0:15353`. These are container addresses and ports. Add a matching `ports` entry, including `/tcp` or `/udp`, for each additional reverse listener. If you change only the published host port, the container-side listener stays unchanged.

When using SOCKS5 or fixed forwarding, destinations are reached from the container's network; `127.0.0.1` refers to the container itself.

Common commands:

```bash
# Show service status
docker compose ps
# Recreate after editing config.yaml
docker compose up -d --force-recreate
# Rebuild after updating source
docker compose up -d --build
# Stop and remove containers; config.yaml remains on the host
docker compose down
```
