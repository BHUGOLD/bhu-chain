package miner

import (
	"sync"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/consensus"
	"github.com/bhu/bhu-chain/core/types"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
)

type CpuAgent struct {
	mu sync.Mutex

	workCh        chan *types.Block
	quit          chan struct{}
	quitCurrentOp chan struct{}
	returnCh      chan<- *types.Block

	index     int
	consensus consensus.BHU
}

func NewCpuAgent(index int, consensus consensus.BHU) *CpuAgent {
	miner := &CpuAgent{
		consensus: consensus,
		index:     index,
	}

	return miner
}

func (self *CpuAgent) Work() chan<- *types.Block          { return self.workCh }
func (self *CpuAgent) BHU() consensus.BHu                 { return self.BHU }
func (self *CpuAgent) SetReturnCh(ch chan<- *types.Block) { self.returnCh = ch }

func (self *CpuAgent) Stop() {
	self.mu.Lock()
	defer self.mu.Unlock()

	close(self.quit)
}

func (self *CpuAgent) Start() {
	self.mu.Lock()
	defer self.mu.Unlock()

	self.quit = make(chan struct{})

	self.workCh = make(chan *types.Block, 1)

	go self.update()
}

func (self *CpuAgent) update() {
out:
	for {
		select {
		case block := <-self.workCh:
			self.mu.Lock()
			if self.quitCurrentOp != nil {
				close(self.quitCurrentOp)
			}
			self.quitCurrentOp = make(chan struct{})
			go self.mine(block, self.quitCurrentOp)
			self.mu.Unlock()
		case <-self.quit:
			self.mu.Lock()
			if self.quitCurrentOp != nil {
				close(self.quitCurrentOp)
				self.quitCurrentOp = nil
			}
			self.mu.Unlock()
			break out
		}
	}

done:

	for {
		select {
		case <-self.workCh:
		default:
			close(self.workCh)

			break done
		}
	}
}

func (self *CpuAgent) mine(block *types.Block, stop <-chan struct{}) {
	glog.V(logger.Debug).Infof("(re)started agent[%d]. mining...\n", self.index)

	nonce, mixDigest := self.consensus.Search(block, stop)
	if nonce != 0 {
		self.returnCh <- block.WithMiningResult(nonce, common.BytesToHash(mixDigest))
	} else {
		self.returnCh <- nil
	}
}

func (self *CpuAgent) GetHashRate() int64 {
	return self.consensus.GetHashrate()
}
