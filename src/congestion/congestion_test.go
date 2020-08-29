package congestion

import (
	"github.com/esrrhs/go-engine/src/rbuffergo"
	"testing"
)

type CongestionTest struct {
	buf         *rbuffergo.Rlistgo
	rtt_ms      int
	bw_mbps     int
	packet_size int
}

func TestBB1(t *testing.T) {
	bb := BBCongestion{}
	bb.Init()

	ct := CongestionTest{}
	ct.buf = rbuffergo.NewRList(1000)
	ct.rtt_ms = 200
	ct.bw_mbps = 10
	ct.packet_size = 500

	//for i := 0; i < 100; i++ {
	//	for {
	//		if !bb.CanSend(0, ct.packet_size) {
	//			break
	//		}
	//		ct.buf.PushBack(0)
	//	}
	//	bb.Update()
	//}
}
