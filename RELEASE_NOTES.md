UDPBrutal 提供明文 UDP 隧道、Hysteria2 Brutal 拥塞控制、本地 SOCKS5（TCP/UDP）、正向 TCP/UDP 和反向 TCP/UDP 端口映射。

代码兼顾 Linux、macOS 和 Windows 的 amd64/arm64；发行归档只在手动选择平台并触发工作流后产生。当前源码提交不自动构建或发布。配置与安装步骤见 README.md 及 examples/。

本项目使用独立明文协议，不与 Hysteria2 客户端或服务端互通。构建流程只执行编译和静态检查，不运行功能测试。此版本的实际代理流量与网络异常行为尚未经过端到端验证。
