package bhu

import (
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/bhu/bhu-chain/bhu/downloader"
	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/core/types"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"github.com/bhu/bhu-chain/p2p"
	"gopkg.in/fatih/set.v0"
)

var (
	errAlreadyRegistered = errors.New("peer is already registered")
	errNotRegistered     = errors.New("peer is not registered")
)

const (
	maxKnownTxs    = 43670
	maxKnownBlocks = 1024
)

type peer struct {
	*p2p.Peer

	rw p2p.MsgReadWriter

	version int
	network int

	id string

	head common.Hash
	td   *big.Int
	lock sync.RWMutex

	knownTxs    *set.Set
	knownBlocks *set.Set
}

func newPeer(version, network int, p *p2p.Peer, rw p2p.MsgReadWriter) *peer {
	id := p.ID()

	return &peer{
		Peer:        p,
		rw:          rw,
		version:     version,
		network:     network,
		id:          fmt.Sprintf("%x", id[:8]),
		knownTxs:    set.New(),
		knownBlocks: set.New(),
	}
}

func (p *peer) Head() (hash common.Hash) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	copy(hash[:], p.head[:])
	return hash
}

func (p *peer) SetHead(hash common.Hash) {
	p.lock.Lock()
	defer p.lock.Unlock()

	copy(p.head[:], hash[:])
}

func (p *peer) Td() *big.Int {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return new(big.Int).Set(p.td)
}

func (p *peer) SetTd(td *big.Int) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.td.Set(td)
}

func (p *peer) MarkBlock(hash common.Hash) {

	for p.knownBlocks.Size() >= maxKnownBlocks {
		p.knownBlocks.Pop()
	}
	p.knownBlocks.Add(hash)
}

func (p *peer) MarkTransaction(hash common.Hash) {

	for p.knownTxs.Size() >= maxKnownTxs {
		p.knownTxs.Pop()
	}
	p.knownTxs.Add(hash)
}

func (p *peer) SendTransactions(txs types.Transactions) error {
	propTxnOutPacketsMeter.Mark(1)
	for _, tx := range txs {
		propTxnOutTrafficMeter.Mark(tx.Size().Int64())
		p.knownTxs.Add(tx.Hash())
	}
	return p2p.Send(p.rw, TxMsg, txs)
}

func (p *peer) SendBlockHashes(hashes []common.Hash) error {
	reqHashOutPacketsMeter.Mark(1)
	reqHashOutTrafficMeter.Mark(int64(32 * len(hashes)))

	return p2p.Send(p.rw, BlockHashesMsg, hashes)
}

func (p *peer) SendBlocks(blocks []*types.Block) error {
	reqBlockOutPacketsMeter.Mark(1)
	for _, block := range blocks {
		reqBlockOutTrafficMeter.Mark(block.Size().Int64())
	}
	return p2p.Send(p.rw, BlocksMsg, blocks)
}

func (p *peer) SendNewBlockHashes(hashes []common.Hash) error {
	propHashOutPacketsMeter.Mark(1)
	propHashOutTrafficMeter.Mark(int64(32 * len(hashes)))

	for _, hash := range hashes {
		p.knownBlocks.Add(hash)
	}
	return p2p.Send(p.rw, NewBlockHashesMsg, hashes)
}

func (p *peer) SendNewBlock(block *types.Block, td *big.Int) error {
	propBlockOutPacketsMeter.Mark(1)
	propBlockOutTrafficMeter.Mark(block.Size().Int64())

	p.knownBlocks.Add(block.Hash())
	return p2p.Send(p.rw, NewBlockMsg, []interface{}{block, td})
}

func (p *peer) RequestHashes(from common.Hash) error {
	glog.V(logger.Debug).Infof("Peer [%s] fetching hashes (%d) from %x...\n", p.id, downloader.MaxHashFetch, from[:4])
	return p2p.Send(p.rw, GetBlockHashesMsg, getBlockHashesData{from, uint64(downloader.MaxHashFetch)})
}

func (p *peer) RequestHashesFromNumber(from uint64, count int) error {
	glog.V(logger.Debug).Infof("Peer [%s] fetching hashes (%d) from #%d...\n", p.id, count, from)
	return p2p.Send(p.rw, GetBlockHashesFromNumberMsg, getBlockHashesFromNumberData{from, uint64(count)})
}

func (p *peer) RequestBlocks(hashes []common.Hash) error {
	glog.V(logger.Debug).Infof("[%s] fetching %v blocks\n", p.id, len(hashes))
	return p2p.Send(p.rw, GetBlocksMsg, hashes)
}

func (p *peer) Handshake(td *big.Int, head common.Hash, genesis common.Hash) error {

	errc := make(chan error, 1)
	go func() {
		errc <- p2p.Send(p.rw, StatusMsg, &statusData{
			ProtocolVersion: uint32(p.version),
			NetworkId:       uint32(p.network),
			TD:              td,
			CurrentBlock:    head,
			GenesisBlock:    genesis,
		})
	}()

	msg, err := p.rw.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Code != StatusMsg {
		return errResp(ErrNoStatusMsg, "first msg has code %x (!= %x)", msg.Code, StatusMsg)
	}
	if msg.Size > ProtocolMaxMsgSize {
		return errResp(ErrMsgTooLarge, "%v > %v", msg.Size, ProtocolMaxMsgSize)
	}

	var status statusData
	if err := msg.Decode(&status); err != nil {
		return errResp(ErrDecode, "msg %v: %v", msg, err)
	}
	if status.GenesisBlock != genesis {
		return errResp(ErrGenesisBlockMismatch, "%x (!= %x)", status.GenesisBlock, genesis)
	}
	if int(status.NetworkId) != p.network {
		return errResp(ErrNetworkIdMismatch, "%d (!= %d)", status.NetworkId, p.network)
	}
	if int(status.ProtocolVersion) != p.version {
		return errResp(ErrProtocolVersionMismatch, "%d (!= %d)", status.ProtocolVersion, p.version)
	}

	p.td, p.head = status.TD, status.CurrentBlock
	return <-errc
}

func (p *peer) String() string {
	return fmt.Sprintf("Peer %s [%s]", p.id,
		fmt.Sprintf("bhu/%2d", p.version),
	)
}

type peerSet struct {
	peers map[string]*peer
	lock  sync.RWMutex
}

func newPeerSet() *peerSet {
	return &peerSet{
		peers: make(map[string]*peer),
	}
}

func (ps *peerSet) Register(p *peer) error {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	if _, ok := ps.peers[p.id]; ok {
		return errAlreadyRegistered
	}
	ps.peers[p.id] = p
	return nil
}

func (ps *peerSet) Unregister(id string) error {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	if _, ok := ps.peers[id]; !ok {
		return errNotRegistered
	}
	delete(ps.peers, id)
	return nil
}

func (ps *peerSet) Peer(id string) *peer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.peers[id]
}

func (ps *peerSet) Len() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return len(ps.peers)
}

func (ps *peerSet) PeersWithoutBlock(hash common.Hash) []*peer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		if !p.knownBlocks.Has(hash) {
			list = append(list, p)
		}
	}
	return list
}

func (ps *peerSet) PeersWithoutTx(hash common.Hash) []*peer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		if !p.knownTxs.Has(hash) {
			list = append(list, p)
		}
	}
	return list
}

func (ps *peerSet) BestPeer() *peer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	var (
		bestPeer *peer
		bestTd   *big.Int
	)
	for _, p := range ps.peers {
		if td := p.Td(); bestPeer == nil || td.Cmp(bestTd) > 0 {
			bestPeer, bestTd = p, td
		}
	}
	return bestPeer
}
