module github.com/asdetycv1zzc/UDPBrutal

go 1.26.0

require (
	github.com/apernet/hysteria/core/v2 v2.0.0
	github.com/apernet/quic-go v0.65.1-0.20261109005002-2895f039137a
	github.com/txthinking/socks5 v0.0.0-20230325130024-4230056ae301
	golang.org/x/net v0.56.0
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/apernet/hysteria/core/v2 => ./third_party/hysteria/core

replace github.com/apernet/quic-go => ./third_party/quic-go

replace github.com/txthinking/socks5 => ./third_party/socks5
