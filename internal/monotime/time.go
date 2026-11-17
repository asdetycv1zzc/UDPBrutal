// Package monotime provides nanoseconds since process start, matching the time
// representation expected by Hysteria's congestion controller.
package monotime

import "time"

var epoch = time.Now()

type Time int64

func Now() Time                             { return Time(time.Since(epoch)) + 1 }
func (t Time) IsZero() bool                 { return t == 0 }
func (t Time) Add(d time.Duration) Time     { return t + Time(d) }
func (t Time) Sub(other Time) time.Duration { return time.Duration(t - other) }
