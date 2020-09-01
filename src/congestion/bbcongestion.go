package congestion

import (
	"github.com/esrrhs/go-engine/src/rbuffergo"
	"strconv"
)

const (
	bbc_status_init = 0
	bbc_status_prop = 1

	bbc_maxfly_win     = 10
	bbc_maxfly_grow    = 2.1
	bbc_maxfly_compare = float64(0.6)
)

var prop_seq = []float64{1, 1, 1.2, 0.7}

type BBCongestion struct {
	status     int
	maxfly     int
	flyeddata  int
	flyingdata int
	maxflywin  *rbuffergo.Rlistgo
	propindex  int
}

func (bb *BBCongestion) Init() {
	bb.status = bbc_status_init
	bb.maxfly = 1024 * 1024
	bb.maxflywin = rbuffergo.NewRList(bbc_maxfly_win)
}

func (bb *BBCongestion) RecvAck(id int, size int) {
	bb.flyeddata += size
}

func (bb *BBCongestion) CanSend(id int, size int) bool {
	if bb.flyingdata > bb.maxfly {
		return false
	}
	bb.flyingdata += size
	return true
}

func (bb *BBCongestion) Update() {

	if bb.status == bbc_status_init {
		if bb.flyeddata > 0 {
			if float64(bb.flyeddata)/float64(bb.maxfly) <= bbc_maxfly_compare {
				bb.status = bbc_status_prop
				//loggo.Debug("bbc_status_init flyeddata %d maxfly %d change", bb.flyeddata, bb.maxfly)
			} else {
				oldmaxfly := bb.maxfly
				bb.maxfly = int(float64(oldmaxfly) * bbc_maxfly_grow)
				//loggo.Debug("bbc_status_init grow flyeddata %d oldmaxfly %d maxfly %d", bb.flyeddata, oldmaxfly, bb.maxfly)
			}
		}
	} else if bb.status == bbc_status_prop {

		if bb.maxflywin.Full() {
			bb.maxflywin.PopFront()
		}
		bb.maxflywin.PushBack(bb.flyeddata)

		lastmaxfly := 0
		for e := bb.maxflywin.FrontInter(); e != nil; e = e.Next() {
			mf := e.Value.(int)
			if mf > lastmaxfly {
				lastmaxfly = mf
			}
		}

		oldmaxfly := lastmaxfly
		bb.maxfly = int(float64(oldmaxfly) * prop_seq[bb.propindex])
		bb.propindex++
		bb.propindex = bb.propindex % len(prop_seq)
		//loggo.Debug("bbc_status_prop lastmaxfly %d oldmaxfly %d maxfly %d prop %v", lastmaxfly, oldmaxfly, bb.maxfly, prop_seq[bb.propindex])
	} else {
		panic("error status " + strconv.Itoa(bb.status))
	}

	bb.flyeddata = 0
	bb.flyingdata = 0
}
