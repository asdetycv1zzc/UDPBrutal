package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/core/v2/extension"
	hyserver "github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

type observedDatagram struct {
	conn *quic.Conn
	data []byte
}

func observeDatagrams(t *testing.T) (func(*quic.Conn, []byte) bool, <-chan observedDatagram) {
	t.Helper()
	packets := make(chan observedDatagram, 1024)
	ctx := t.Context()
	return func(conn *quic.Conn, data []byte) bool {
		select {
		case packets <- observedDatagram{conn: conn, data: bytes.Clone(data)}:
		case <-ctx.Done():
		}
		return false // Keep the ordinary HY2 UDP relay active.
	}, packets
}

func datagramTestServer(t *testing.T, disablePMTU bool) (net.Addr, <-chan observedDatagram) {
	t.Helper()
	observe, packets := observeDatagrams(t)
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	server, err := hyserver.NewServer(&hyserver.Config{
		Conn: socket, Authenticator: passwordAuth{config.Auth{Type: "none"}},
		QUICConfig:      hyserver.QUICConfig{DisablePathMTUDiscovery: disablePMTU, DisableGSO: true},
		DatagramHandler: observe,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve() }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})
	return socket.LocalAddr(), packets
}

// Use the real core factories so these tests cover the transport parameters
// actually selected by UDPBrutal, in addition to native packet delivery.
func datagramTestPair(t *testing.T, disablePMTU bool) (hyclient.Client, *quic.Conn, <-chan observedDatagram, <-chan observedDatagram) {
	t.Helper()
	addr, serverPackets := datagramTestServer(t, disablePMTU)
	observe, clientPackets := observeDatagrams(t)
	client, _, err := hyclient.NewClient(&hyclient.Config{
		ServerAddr:      addr,
		QUICConfig:      hyclient.QUICConfig{DisablePathMTUDiscovery: disablePMTU, DisableGSO: true},
		DatagramHandler: observe,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	conn := client.(interface{ Connection() *quic.Conn }).Connection()
	return client, conn, serverPackets, clientPackets
}

func nextDatagram(t *testing.T, packets <-chan observedDatagram) observedDatagram {
	t.Helper()
	select {
	case packet := <-packets:
		return packet
	case <-time.After(5 * time.Second):
		t.Fatal("timed out receiving DATAGRAM")
		return observedDatagram{}
	}
}

func TestDatagramReceiveLimitUsesPathCapacity(t *testing.T) {
	_, conn, serverPackets, clientPackets := datagramTestPair(t, true)
	// The default 1280-byte initial packet can hold this payload, but a
	// 1200-byte peer frame limit cannot. No MTU probing is needed for this case.
	payload := bytes.Repeat([]byte{0xa5}, 1220)
	if err := conn.SendDatagram(payload); err != nil {
		t.Fatalf("client rejected a payload that fits the path: %v", err)
	}
	packet := nextDatagram(t, serverPackets)
	if !bytes.Equal(packet.data, payload) {
		t.Fatal("server received a different DATAGRAM payload")
	}
	if err := packet.conn.SendDatagram(payload); err != nil {
		t.Fatalf("server rejected a payload that fits the path: %v", err)
	}
	if !bytes.Equal(nextDatagram(t, clientPackets).data, payload) {
		t.Fatal("client received a different DATAGRAM payload")
	}
	// Advertising a large receive capability must not bypass the path limit.
	for _, sender := range []*quic.Conn{conn, packet.conn} {
		var tooLarge *quic.DatagramTooLargeError
		if err := sender.SendDatagram(make([]byte, 3000)); !errors.As(err, &tooLarge) {
			t.Fatalf("expected a path-size error, got %v", err)
		}
		if tooLarge.MaxDatagramPayloadSize < int64(len(payload)) || tooLarge.MaxDatagramPayloadSize >= 1280 {
			t.Fatalf("unexpected initial path capacity: %d", tooLarge.MaxDatagramPayloadSize)
		}
	}
}

func TestDatagramPayloadGrowsWithPMTU(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("path MTU discovery is disabled on this platform")
	}
	_, conn, serverPackets, clientPackets := datagramTestPair(t, false)
	if err := conn.SendDatagram([]byte{0}); err != nil {
		t.Fatal(err)
	}
	serverConn := nextDatagram(t, serverPackets).conn
	payload := bytes.Repeat([]byte{0xb6}, 1330)
	for _, direction := range []struct {
		name    string
		conn    *quic.Conn
		packets <-chan observedDatagram
	}{{"upload", conn, serverPackets}, {"download", serverConn, clientPackets}} {
		t.Run(direction.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				err := direction.conn.SendDatagram(payload)
				if err == nil {
					break
				}
				var tooLarge *quic.DatagramTooLargeError
				if !errors.As(err, &tooLarge) {
					t.Fatal(err)
				}
				// Keep application traffic flowing while the native engine probes.
				if err := direction.conn.SendDatagram([]byte{0}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatalf("PMTU did not permit a %d-byte payload: %v", len(payload), err)
				case <-ticker.C:
				}
			}
			for {
				select {
				case packet := <-direction.packets:
					if bytes.Equal(packet.data, payload) {
						return
					}
				case <-ctx.Done():
					t.Fatal("larger DATAGRAM was not delivered after MTU probing")
				}
			}
		})
	}
}

func TestDatagramLegacyReceiveLimits(t *testing.T) {
	for _, omit := range []bool{true, false} {
		name := "advertised_1200"
		if omit {
			name = "omitted_parameter"
		}
		t.Run(name, func(t *testing.T) {
			addr, packets := datagramTestServer(t, true)
			socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = socket.Close() })
			transport := &quic.Transport{Conn: socket, DisableGSO: true}
			t.Cleanup(func() { _ = transport.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, err := transport.Dial(ctx, addr, &tls.Config{}, &quic.Config{
				EnableDatagrams: true, MaxDatagramFrameSize: 1200,
				OmitMaxDatagramFrameSize: omit, DisablePathMTUDiscovery: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
			httpTransport := &http3.Transport{}
			t.Cleanup(func() { _ = httpTransport.Close() })
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://hysteria/auth", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := httpTransport.NewClientConn(conn).RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 233 {
				t.Fatalf("legacy authentication failed: %d", response.StatusCode)
			}
			if err := conn.SendDatagram([]byte{0}); err != nil {
				t.Fatal(err)
			}
			serverConn := nextDatagram(t, packets).conn
			var tooLarge *quic.DatagramTooLargeError
			if err := serverConn.SendDatagram(make([]byte, 1220)); !errors.As(err, &tooLarge) {
				t.Fatalf("legacy receive limit was ignored: %v", err)
			}
			if tooLarge.MaxDatagramPayloadSize != 1197 {
				t.Fatalf("unexpected legacy payload limit: %d", tooLarge.MaxDatagramPayloadSize)
			}
			payload := bytes.Repeat([]byte{0xc7}, 1197)
			if err := serverConn.SendDatagram(payload); err != nil {
				t.Fatal(err)
			}
			data, err := conn.ReceiveDatagram(ctx)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("legacy DATAGRAM delivery failed: %v", err)
			}
		})
	}
}

func TestUDPDatagramFragmentation(t *testing.T) {
	client, _, serverPackets, clientPackets := datagramTestPair(t, true)
	echo, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		buffer := make([]byte, extension.MaxUDPSize)
		for {
			n, addr, err := echo.ReadFrom(buffer)
			if err != nil {
				return
			}
			if _, err := echo.WriteTo(buffer[:n], addr); err != nil {
				return
			}
		}
	}()
	udp, err := client.UDP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	// Leave room for the address header in the core's 4096-byte send buffer.
	for _, size := range []int{1200, 3000, 4000} {
		payload := bytes.Repeat([]byte{byte(size % 251)}, size)
		if err := udp.Send(payload, echo.LocalAddr().String()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			data, _, err := udp.Receive()
			if err == nil && !bytes.Equal(data, payload) {
				err = errors.New("UDP echo payload differs after reassembly")
			}
			result <- err
		}()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out echoing %d-byte UDP payload", size)
		}
		for _, packets := range []<-chan observedDatagram{serverPackets, clientPackets} {
			message, err := extension.ParseUDPMessage(nextDatagram(t, packets).data)
			if err != nil {
				t.Fatal(err)
			}
			if (size == 1200 && message.FragCount != 1) || (size > 1200 && message.FragCount <= 1) {
				t.Fatalf("%d-byte UDP payload used %d fragments", size, message.FragCount)
			}
			for i := uint8(1); i < message.FragCount; i++ {
				nextDatagram(t, packets)
			}
		}
	}
}

func TestReverseUDPDatagramFragmentation(t *testing.T) {
	_, conn, serverPackets, clientPackets := datagramTestPair(t, true)
	if err := conn.SendDatagram([]byte{0}); err != nil {
		t.Fatal(err)
	}
	serverConn := nextDatagram(t, serverPackets).conn
	for _, direction := range []struct {
		conn    *quic.Conn
		packets <-chan observedDatagram
	}{{conn, serverPackets}, {serverConn, clientPackets}} {
		sender := extension.UDPSender{Conn: direction.conn, Prefix: reversePrefix(7)}
		for _, size := range []int{1200, 3000, 4000} {
			payload := bytes.Repeat([]byte{0xd8}, size)
			message := extension.UDPMessage{SessionID: 9, FragCount: 1, Addr: "127.0.0.1:1", Data: payload}
			if err := sender.Send(&message); err != nil {
				t.Fatal(err)
			}
			var defragger extension.Defragger
			var complete *extension.UDPMessage
			count := uint8(1)
			for i := uint8(0); i < count; i++ {
				packet := nextDatagram(t, direction.packets)
				if len(packet.data) >= 1280 {
					t.Fatalf("reverse prefix exceeded the initial path capacity: %d", len(packet.data))
				}
				index, fragment, ok := parseReverseDatagram(packet.data)
				if !ok || index != 7 || fragment == nil {
					t.Fatal("invalid reverse UDP fragment")
				}
				count = fragment.FragCount
				complete = defragger.Feed(fragment)
			}
			if (size == 1200 && count != 1) || (size > 1200 && count <= 1) {
				t.Fatalf("%d-byte reverse UDP payload used %d fragments", size, count)
			}
			if complete == nil || !bytes.Equal(complete.Data, payload) {
				t.Fatalf("%d-byte reverse UDP payload did not reassemble", size)
			}
		}
	}
}
