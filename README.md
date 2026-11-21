# UDPBrutal

[![License][1]][2] [![Go][3]][4] [![Documentation][5]][6] [![Transport][7]][8]

[1]: https://img.shields.io/badge/license-MIT-blue
[2]: LICENSE
[3]: https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat-square
[4]: go.mod
[5]: https://img.shields.io/badge/docs-get%20started-blue?style=flat-square
[6]: docs/README.md
[7]: https://img.shields.io/badge/transport-plaintext%20UDP-orange?style=flat-square
[8]: PROTOCOL.md

<h2 align="center">UDPBrutal is a plaintext UDP proxy powered by Hysteria2's Brutal congestion control.</h2>

### [Get Started](docs/README.md)

### [中文文档](docs/README.zh.md)

### [Protocol Specification](PROTOCOL.md)

---

<div class="feature-grid">
  <div>
    <h3>🛠️ Flexible proxy modes</h3>
    <p>Local SOCKS5 with TCP and UDP support, plus fixed TCP/UDP forwarding for applications and services.</p>
  </div>

  <div>
    <h3>🔄 Forward and reverse</h3>
    <p>Reach remote networks through the server, or expose local TCP and UDP services on server ports over a client-initiated tunnel.</p>
  </div>

  <div>
    <h3>⚡ Brutal at its core</h3>
    <p>The pinned Hysteria2 core with native streams, DATAGRAMs, flow control and path MTU discovery. Brutal supports live bandwidth updates and up to 1.33× loss compensation.</p>
  </div>

  <div>
    <h3>💻 Cross-platform</h3>
    <p>Written in Go with standard networking interfaces for Linux, macOS, and Windows. Platform and architecture can be selected when manually building a release.</p>
  </div>

  <div>
    <h3>🔓 Plaintext by design</h3>
    <p>A plaintext profile of Hysteria2's QUIC engine, without TLS, certificates, packet encryption, or obfuscation.</p>
  </div>

  <div>
    <h3>📖 Ready to explore</h3>
    <p>Configuration examples, installation instructions, a protocol specification, and pinned upstream references are included in the repository.</p>
  </div>
</div>

---

### Docker Compose

Copy the server configuration from the repository root:

```bash
cp examples/server.yaml config.yaml
```

Edit `config.yaml` and use the same `auth` value on the client, then start the server:

```bash
docker compose up -d --build
```

The example publishes UDP `443`, reverse TCP `18080`, and reverse UDP `15353`. See the [English](docs/README.md#docker-compose) or [中文](docs/README.zh.md#docker-compose) guide for client mappings, logs, and shutdown.

Module download timed out? See the [build proxy settings](docs/README.md#module-download-timeouts) / [依赖下载超时](docs/README.zh.md#依赖下载超时), including Compose build arguments.

---

UDPBrutal uses a dedicated plaintext handshake and packet profile and does not interoperate with unmodified Hysteria2. Source attribution and implementation boundaries are documented in [NOTICE.md](NOTICE.md).

Builds are manual. Linux loopback regression tests cover DATAGRAM negotiation, PMTU growth and fragmentation; see the [validation scope](docs/README.md#validation-scope).

**⭐ Star UDPBrutal if it helps your project.**
