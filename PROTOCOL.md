# UDPBrutal wire protocol v1

This is a standalone plaintext protocol. It does not implement the Hysteria2
QUIC/HTTP3 wire protocol. There are no keys, ciphers, MACs, certificates, TLS,
encrypted packets, obfuscation, or application-level cryptographic checksums.
The operating system supplies ordinary UDP/IP checksums.

## UDP packet

All integer fields are unsigned big endian. The maximum UDP payload is 1200
bytes, including the 37-byte header.

| Offset | Bytes | Meaning |
| --- | --- | --- |
| 0 | 4 | ASCII UB01 |
| 4 | 1 | Kind: HELLO=1, WELCOME=2, DATA=3, ACK=4, PING=5, PONG=6, GOODBYE=7, REJECT=8 |
| 5 | 8 | Session ID, scoped to the peer IP and UDP port |
| 13 | 8 | Reliable DATA sequence number, starting at 1; ACK echoes the sequence |
| 21 | 8 | Transmission number, unique on every DATA send including retransmissions; ACK echoes it |
| 29 | 8 | Next reliable DATA sequence the application will consume (receiver window base) |
| 37 | remaining | Packet payload |

HELLO payload is JSON: `{"auth":"string","tx":12500000,"rx":12500000}`.
Rates are bytes per second; tx must be positive. rx=0 means no extra receive cap.
WELCOME returns the negotiated client tx in `tx` and negotiated server tx in
`rx`. Server tx is min(server up, client down), client tx is min(client up,
server down), with zero receive caps ignored. Authentication is a direct string
comparison, and the token is transmitted literally. REJECT contains a reason.
HELLO is retried every 200ms until the handshake timeout. An existing session
responds idempotently to repeated HELLO.

DATA carries up to 1163 consecutive bytes. The receiver accepts sequences in
`[next, next+512)`, suppresses duplicates, ACKs accepted/previously consumed DATA,
and returns ordered bytes to the multiplexer. Sequences beyond the receive
window are discarded without ACK. The sender also limits unacknowledged queued
DATA and its sending sequence to the advertised peer window. Consumption sends
an ACK with zero sequence/number to advance the window. PING/PONG refresh it if
a window update is lost.

Each DATA transmission, including retransmissions, consumes the original
Hysteria Brutal congestion window and pacing budget. RTT is sampled from
acknowledged original sends; RTO starts at 200ms, then uses SRTT + 4*RTTVAR with
an 80ms..2s clamp, exponential backoff, and a 30-transmission limit per DATA
sequence. Retransmission expiry reports a loss to Brutal; matching ACK reports
an acknowledged transmission. The controller keeps the upstream five sampling
slots and ACK-rate clamp. ACK, handshake and keepalive packets are not paced.

PING is sent every 2s. A peer silent longer than idleTimeout closes the session.
GOODBYE closes the complete tunnel; it is best effort. Individual TCP connection
closure is carried by reliable multiplexed stream FINs instead.

## Multiplexing and requests

The ordered plaintext connection runs yamux v0.1.2 with its normal client/server
stream-ID ownership, stream flow control, and half-close semantics. This adds
no encryption. It shares one reliable UDP sequence space, so loss can block
all streams until retransmission succeeds.

Every stream starts with a 4-byte JSON byte length followed by JSON (maximum
16384 bytes). Responses use the same framing: `{"ok":true}` or
`{"ok":false,"error":"reason"}`. Normal transfer begins only after success.

| Request command | Direction | Meaning |
| --- | --- | --- |
| control | Client to server, first stream only | Register reverse TCP/UDP listeners; keep stream open for session lifetime |
| tcp | Client to server | Dial address (host:port) from server, then relay raw bytes |
| udp | Client to server | Create an unconnected UDP socket for SOCKS5 UDP ASSOCIATE |
| udpFixed | Client to server | Create a connected UDP socket to address for local fixed forwarding |
| reverse | Server to client | Dial the local TCP destination selected by index from client's reverseTCP config |
| reverseUDP | Server to client | Create a connected local UDP socket selected by index from client's reverseUDP config |

The control request carries `reverse` and `reverseUDP` arrays of `{listen}`
objects, with optional UDP `timeout` as integer nanoseconds. The server binds
all listeners or rolls back all of them on failure, enforces reverse.enabled
and maxListeners, and returns actual TCP `addresses` and UDP `udpAddresses`.
Local destination addresses are selected solely from the client's own config;
the server does not supply arbitrary local target addresses. On tunnel teardown
listeners and their source associations are released. Reconnection registers a
new set and does not resume existing streams.

TCP transfer is byte-for-byte with independent directional EOF. The connected
destination sees the proxy's source address, not the original caller's address.

UDP records within a stream consist of uint16 address length, uint16 payload
length, address bytes, then payload bytes. Address is host:port and at most
512 bytes; payload may be empty and is at most 65507 bytes. UDP packet boundaries
are preserved, but delivery across the tunnel is reliable and ordered. SOCKS5
UDP supports per-datagram destination addresses. Fixed forwarding ignores the
record's destination field and uses its configured target.

For each UDP mapping, each original source IP:port receives a separate stream
and connected destination UDP socket. Replies travel on that stream and are
sent from the mapping's listening socket to the original source. A mapping is
limited to 256 active source associations and each source has a 32-datagram
pending queue; excess datagrams are dropped. Both traffic directions reset the
idle timer. The client SOCKS UDP association also lives no longer than its TCP
control connection.
