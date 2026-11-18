# UDPBrutal HY2 plaintext profile

UDPBrutal embeds the Hysteria core at
`c2b181e0b0e72b077e4b0b7297735af14d1f9929` and its quic-go dependency at
`2895f039137a405a34cffbcb27510ad31717d89a`. The QUIC connection engine,
ACK/loss detection, pacing integration, stream and connection flow control,
stream multiplexing, DATAGRAM delivery, and path-MTU discovery are the pinned
source implementations. The former UB01 reliable byte stream and yamux layer
have been removed.

This is a dedicated **plaintext profile**, not standard QUIC and not compatible
with an unmodified Hysteria2 peer. It contains no TLS handshake, packet/header
encryption, cryptographic tags, certificate handling, key derivation, key
rotation, encrypted address tokens, or encrypted session tickets. Public
`tls.Config`/`tls.ConnectionState` types remain for source/API compatibility
with the embedded HTTP/3 implementation; no TLS connection is created and no
TLS handshake completion is reported.

## Packet profile

The source QUIC v1 packet and frame layouts are retained. The version field is
`0x55425031` (UBP1), rather than advertising a standard encrypted QUIC version.
Payload adapters copy bytes without an authentication tag, and header adapters
leave all header bytes unchanged. Packet-number decoding is the original
upstream algorithm. The Initial, Handshake, and 1-RTT packet-number spaces and
their engine lifecycle remain distinct; those names denote phases in this
profile and do not imply encryption.

Packet packing and unpacking skip the encrypted profile's 16-byte header
protection sample. Native packet-number and payload padding remain in place;
small ACK/PING packets do not require a nonexistent authentication tag.

Retry integrity fields consist of 16 zero padding bytes. Address tokens carry
their original ASN.1 records literally. Stateless-reset tokens are literal
connection IDs padded to 16 bytes. These fields have no cryptographic
authenticity or ownership guarantee.

There is no fixed 1200-byte ceiling on the outer UDP payload. The source
`InitialPacketSize=1280`, the source EMSGSIZE fallback, peer limits, platform
DF support, and the source MTU discoverer determine packet sizing. QUIC's
1200-byte minimum and Hysteria's separate `MaxDatagramFrameSize=1200` must not
be confused with a fixed outer packet limit. The MTU discoverer and congestion
controller adapters are unchanged source files. The recovery owner has optional
diagnostic counter hooks; its recovery decisions remain the pinned algorithms.

When GSO is disabled on Linux, the socket sender can submit up to eight already
queued datagrams with sendmmsg. Each datagram retains its own payload boundary,
destination and control messages. This adds no batch-fill delay or UDP_SEGMENT
request and does not change packet packing, pacing or congestion decisions.
Partial sends continue only with the unaccepted suffix; ordinary writes handle
errors and unsupported platforms. This IO optimization does not change the wire
format or require an additional protocol negotiation.

Optional diagnostics also count socket-wrapper datagrams before connection-ID
dispatch, completed handler calls and unmatched short-header IDs. These counters
cover the initial shared Transport, not individual peers or later migration
sockets. Linux kernel queue/drop samples are read by the diagnostic caller from
proc using the socket inode, with a process-wide one-second cache. The receive
path only updates counters/stages; no socket options, packet contents or receive
wakeups are changed. Other platforms report kernel samples as unavailable.

## Plaintext handshake

The upstream reliable CRYPTO streams carry the replacement handshake records.
A record is uint32 big-endian body length, ASCII `UDPBrutal-HY2-P1`, one
message-kind byte, then optional transport-parameter bytes. The body is bounded
to 65536 bytes; the original CRYPTO-stream offset limit also applies.

1. The client sends ClientHello (kind 1) with the original client transport
   parameter serialization on the Initial stream.
2. The server reads those parameters, makes the Handshake-phase adapters
   available, and sends ServerHello (kind 2) with its serialized transport
   parameters on Initial, plus ServerFinished (kind 3) on Handshake.
3. The client waits for ServerFinished before sending ClientFinished (kind 4)
   on Handshake, then announces application readiness to the engine.
4. The server completes only after receiving ClientFinished. The native
   engine sends HANDSHAKE_DONE and applies its normal confirmation, ACK/loss,
   retransmission, packet-space disposal, and MTU-discovery transitions.

This preserves the reliable delivery and ordering machinery; it replaces the
TLS-dependent exchange rather than approximating QUIC with a separate ARQ.
TLS-specific resumption, 0-RTT keys, ClientHello fingerprinting/scrambling, and
key rotation are unavailable.

## Forward proxy protocol

The original Hysteria core performs HTTP/3 framing and the POST /auth exchange,
including bandwidth negotiation and status 233. The HTTP/3 `https` scheme and
`h3` API metadata are retained for the embedded framing implementation; they
do not provide or start TLS in this profile.

Forward TCP is an upstream bidirectional stream with the original 0x401 request
and response serialization. Forward UDP uses the original UDPMessage structure
and an unreliable native DATAGRAM, together with the original fragmenter,
defragmenter, session managers, and timeout behavior. UDP payloads do not travel
through a reliable byte stream and do not acquire cross-stream retransmission
head-of-line blocking. The upstream application buffers and limits, including
`MaxUDPSize=4096`, remain unchanged.

Brutal is selected by the original bandwidth negotiation. If bandwidth is
unknown or the server selects automatic control, the original BBR/Reno
selection remains available. Brutal's maximum loss-compensation factor is
1.33x, replacing the upstream 1.25x cap. Its minimum ACK rate is defined as
`1 / 1.33`; the remaining controller logic and the entire pacer retain the
pinned source implementation.

Once the upstream minimum sample count is met, the adaptive factor is
`1 / max(ACK rate, 1 / 1.33)`. It applies to the local sender's pacing rate
and congestion window for both native streams and DATAGRAMs. Setting
`bandwidth.disableLossCompensation: true` keeps the factor at 1.0. For a
negotiated 300 mbps rate, the maximum compensated sending budget is 399 mbps;
this is not a guarantee of delivered application throughput or an IP/UDP
wire-rate limit. UDP DATAGRAM loss is still not retransmitted or FEC-recovered.

## Reverse extension

Upstream Hysteria does not define the server-port registration described here.
UDPBrutal adds it using native streams and native DATAGRAMs. The extension
does not substitute another transport engine for them.

Authenticated clients register listeners on a bidirectional stream whose first
QUIC varint is 0x554201. It is followed by a uint32 JSON length and a JSON
object with tcp/udp lists of listen addresses (and optional UDP timeout in
nanoseconds). Local target addresses remain in the client's configuration.
The server binds all requested listeners or closes all of them on failure,
enforces reverse.enabled/maxListeners, and replies with actual addresses.
The control stream owns the listeners for its lifetime.

For each incoming reverse TCP connection the server opens a native
bidirectional stream, sends varint 0x554202 and a varint mapping index, then
uses the original Hysteria TCPResponse format. The client dials its configured
local target and both sides forward bytes. Client-side incoming bidirectional
streams are explicitly enabled when reverse TCP is configured, because the
upstream HTTP/3 client's default disables them.

Reverse UDP DATAGRAMs start with eight literal bytes
`55 42 52 45 56 31 00 00`, uint16 mapping index, then the upstream UDPMessage.
Byte 7 is zero, which is reserved away from valid ordinary Hysteria fragments.
Each original source IP:port gets a distinct SessionID and a connected local
UDP socket. Responses retain that SessionID and return to the original source
through the server listener. Oversized frames use the original Hysteria
fragmentation functions after accounting for the extension prefix.

UDP source associations expire on their configured idle timeout. Connection
or control-stream closure releases listeners and associations. Reconnection
registers a new set; existing TCP streams are not resumed.

Reverse UDP resource settings are local to each endpoint. Server session IDs
are unique within a mapping; the mapping index and SessionID identify a route.
Sending reuses serialization storage while keeping the original codec and
fragmenter. Local queues have packet and shared byte budgets and drop new
datagrams when full. Server socket writes occur in per-mapping workers, not
in the connection-wide DATAGRAM reader. Client maps are sharded, with shared
deadline-based idle expiry instead of per-session timers or full-table scans.

Linux reverse-port IO supports recvmmsg/sendmmsg via x/net's batch APIs;
other platforms and unsupported batch syscalls use ordinary UDP sockets.
Batch writes drain already available messages without waiting to fill a batch.
These changes retain the existing UBP1 wire format, native unreliable DATAGRAM
semantics, and the Brutal/pacer integration described above. Reverse application
datagrams larger than MaxUDPSize are dropped, rather than forwarding truncated
data.

## Verification boundary

UDPBRUTAL_DIAGNOSTICS enables optional local transport and reverse-TCP IO
observers. This does not change the UBP1 wire format, congestion controller,
loss recovery, or flow-control algorithm. Owner-loop snapshots are passive and
do not wake the sender; their age must be considered when reading the logs.
The local observer adds counters, clock reads and nonblocking stream snapshots
only when enabled, and is disabled by default. Diagnostics do not establish a
stall's cause or fix it without observations from the affected deployment.

This transport rewrite has not been compiled, built into images, or exercised
with end-to-end traffic. Source hashing, parsing/formatting, module resolution,
and static review are the checks performed for this revision. These checks
do not establish runtime correctness.
