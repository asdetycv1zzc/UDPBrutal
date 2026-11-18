package extension

import (
	"errors"
	"math"

	"github.com/apernet/hysteria/core/v2/internal/congestion/brutal"
	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/congestion"
)

// BandwidthReloadHeader negotiates the optional UDPBrutal control stream.
const BandwidthReloadHeader = "UDPBrutal-Bandwidth-Reload"

// UpdateBrutalBandwidth retains the active sender instead of resetting its
// loss samples, RTT or discovered MTU by installing a replacement controller.
func UpdateBrutalBandwidth(conn *quic.Conn, tx uint64, disableLossCompensation bool) error {
	if tx == 0 || tx > math.MaxInt64 {
		return errors.New("bandwidth reload requires a positive fixed sending rate")
	}
	return conn.UpdateCongestionControl(func(cc congestion.CongestionControl) error {
		sender, ok := cc.(*brutal.BrutalSender)
		if !ok {
			return errors.New("bandwidth reload requires the Brutal controller")
		}
		sender.UpdateBandwidth(tx, disableLossCompensation)
		return nil
	})
}
