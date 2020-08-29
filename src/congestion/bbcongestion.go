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
	bbc_maxfly_compare = float64(1.5)
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

	lastmaxfly := 0
	for e := bb.maxflywin.FrontInter(); e != nil; e = e.Next() {
		mf := e.Value.(int)
		if mf > lastmaxfly {
			lastmaxfly = mf
		}
	}

	bb.maxflywin.PushBack(bb.flyeddata)

	if bb.status == bbc_status_init {
		if lastmaxfly > 0 && float64(bb.flyeddata)/float64(lastmaxfly) <= bbc_maxfly_compare {
			bb.status = bbc_status_prop
		} else {
			bb.maxfly = int(float64(bb.maxfly) * bbc_maxfly_grow)
		}
	} else if bb.status == bbc_status_prop {
		bb.maxfly = lastmaxfly
		bb.maxfly = int(float64(bb.maxfly) * prop_seq[bb.propindex])
	} else {
		panic("error status " + strconv.Itoa(bb.status))
	}

	bb.flyeddata = 0
	bb.flyingdata = 0
}
