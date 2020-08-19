package bhu

import (
	"math/rand"
	"time"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/core/types"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"github.com/bhu/bhu-chain/p2p/discover"
)

const (
	forceSyncCycle      = 10 * time.Second
	minDesiredPeerCount = 5

	txsyncPackSize = 100 * 1024
)

type txsync struct {
	p   *peer
	txs []*types.Transaction
}

func (pm *ProtocolManager) syncTransactions(p *peer) {
	txs := pm.txpool.GetTransactions()
	if len(txs) == 0 {
		return
	}
	select {
	case pm.txsyncCh <- &txsync{p, txs}:
	case <-pm.quitSync:
	}
}

func (pm *ProtocolManager) txsyncLoop() {
	var (
		pending = make(map[discover.NodeID]*txsync)
		sending = false
		pack    = new(txsync)
		done    = make(chan error, 1)
	)

	send := func(s *txsync) {

		size := common.StorageSize(0)
		pack.p = s.p
		pack.txs = pack.txs[:0]
		for i := 0; i < len(s.txs) && size < txsyncPackSize; i++ {
			pack.txs = append(pack.txs, s.txs[i])
			size += s.txs[i].Size()
		}

		s.txs = s.txs[:copy(s.txs, s.txs[len(pack.txs):])]
		if len(s.txs) == 0 {
			delete(pending, s.p.ID())
		}

		glog.V(logger.Detail).Infof("%v: sending %d transactions (%v)", s.p.Peer, len(pack.txs), size)
		sending = true
		go func() { done <- pack.p.SendTransactions(pack.txs) }()
	}

	pick := func() *txsync {
		if len(pending) == 0 {
			return nil
		}
		n := rand.Intn(len(pending)) + 1
		for _, s := range pending {
			if n--; n == 0 {
				return s
			}
		}
		return nil
	}

	for {
		select {
		case s := <-pm.txsyncCh:
			pending[s.p.ID()] = s
			if !sending {
				send(s)
			}
		case err := <-done:
			sending = false

			if err != nil {
				glog.V(logger.Debug).Infof("%v: tx send failed: %v", pack.p.Peer, err)
				delete(pending, pack.p.ID())
			}

			if s := pick(); s != nil {
				send(s)
			}
		case <-pm.quitSync:
			return
		}
	}
}

func (pm *ProtocolManager) syncer() {

	pm.fetcher.Start()
	defer pm.fetcher.Stop()
	defer pm.downloader.Terminate()

	forceSync := time.Tick(forceSyncCycle)
	for {
		select {
		case <-pm.newPeerCh:

			if pm.peers.Len() < minDesiredPeerCount {
				break
			}
			go pm.synchronise(pm.peers.BestPeer())

		case <-forceSync:

			go pm.synchronise(pm.peers.BestPeer())

		case <-pm.quitSync:
			return
		}
	}
}

func (pm *ProtocolManager) synchronise(peer *peer) {

	if peer == nil {
		return
	}

	if peer.Td().Cmp(pm.chainman.Td()) <= 0 {
		return
	}

	pm.downloader.Synchronise(peer.id, peer.Head(), peer.Td())
}
