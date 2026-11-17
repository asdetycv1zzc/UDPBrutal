# Getting Started

[中文文档](README.zh.md) · [Project Home](../README.md)

UDPBrutal provides a local SOCKS5 proxy, fixed TCP/UDP forwarding, and reverse TCP/UDP port mapping. Client and server communicate over a standalone plaintext UDP tunnel using Hysteria2's Brutal congestion controller.

## Installation

Go 1.23 or newer is required to build from source. Access to a private repository requires an authorized GitHub account.

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

Each UDP source gets an independent stream and connected target socket. Idle associations expire after the configured timeout. Reverse listeners are registered together; a bind failure rolls back that registration. Listeners are released when their tunnel closes and registered again after reconnection. Existing TCP streams do not survive a reconnect.

Allow the server's tunnel UDP port and the TCP/UDP mapping ports through the surrounding network. The client initiates the tunnel and needs no public inbound port.

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

`bandwidth.up` sets the local sending rate; `bandwidth.down` caps the peer's sending rate. Units are decimal bps/kbps/mbps/gbps/tbps. The sending default is 100 mbps. Omitting a receive cap adds no extra limit. Set `bandwidth.disableLossCompensation: true` to keep Brutal's ACK rate at 1.

The UDP tunnel defaults to a 30-second idle timeout and 2-second keepalive. Forwarded UDP associations default to a 60-second idle timeout. Configuration parsing rejects unknown fields.

## Compatibility

The command names, configuration layout, and Linux installation layout follow Hysteria2 conventions. UDPBrutal uses an independent plaintext transport and requires UDPBrutal at both ends.

Hysteria2's TLS, ACME, obfuscation, QUIC, HTTP proxy, TUN, transparent proxy, ACL, and BBR configuration are outside this implementation. A complete Hysteria2 configuration cannot be used directly.

UDP datagrams retain their boundaries but travel through reliable multiplexed streams. Tunnel loss therefore causes retransmissions and shared head-of-line blocking, unlike Hysteria2's QUIC DATAGRAM delivery. MTU discovery and NAT address migration are not implemented.

See [PROTOCOL.md](../PROTOCOL.md) for wire behavior and [NOTICE.md](../NOTICE.md) for the pinned source references and adaptation boundaries.

## Validation Scope

Local compilation and static analysis have been completed. Proxy traffic, lossy networks, and multiple-source forwarding have not been verified end to end. Cross-platform runtime support remains unverified. The repository includes no functional test suite, and builds require a manual request.

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
