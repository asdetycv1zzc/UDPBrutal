# UDPBrutal

[![License][1]][2] [![Go][3]][4] [![Documentation][5]][6] [![Transport][7]][8]

[1]: https://img.shields.io/badge/license-MIT-blue
[2]: LICENSE
[3]: https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square
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
    <p>The original Hysteria2 Brutal controller and pacer, with configurable bandwidth and ACK-rate loss compensation.</p>
  </div>

  <div>
    <h3>💻 Cross-platform</h3>
    <p>Written in Go with standard networking interfaces for Linux, macOS, and Windows. Platform and architecture can be selected when manually building a release.</p>
  </div>

  <div>
    <h3>🔓 Plaintext by design</h3>
    <p>A standalone UDP transport without TLS, certificates, encryption, or obfuscation. Authentication strings and tunnel payloads travel in plaintext.</p>
  </div>

  <div>
    <h3>📖 Ready to explore</h3>
    <p>Configuration examples, installation instructions, a protocol specification, and pinned upstream references are included in the repository.</p>
  </div>
</div>

---

UDPBrutal uses its own wire protocol and does not interoperate with Hysteria2. Source attribution and implementation boundaries are documented in [NOTICE.md](NOTICE.md).

Builds are manual. Runtime behavior has not yet been verified end to end; see the [validation scope](docs/README.md#validation-scope).

**⭐ Star UDPBrutal if it helps your project.**
