package miner

import (
	"fmt"
	"math/big"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhu/bhu-chain/accounts"
	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/consensus"
	"github.com/bhu/bhu-chain/core"
	"github.com/bhu/bhu-chain/core/state"
	"github.com/bhu/bhu-chain/core/types"
	"github.com/bhu/bhu-chain/event"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"gopkg.in/fatih/set.v0"
)

var jsonlogger = logger.NewJsonLogger()

type Work struct {
	Number    uint64
	Nonce     uint64
	MixDigest []byte
	SeedHash  []byte
}

type Agent interface {
	Work() chan<- *types.Block
	SetReturnCh(chan<- *types.Block)
	Stop()
	Start()
	GetHashRate() int64
}

const miningLogAtDepth = 5

type uint64RingBuffer struct {
	ints []uint64
	next int
}

type environment struct {
	state              *state.StateDB
	coinbase           *state.StateObject
	ancestors          *set.Set
	family             *set.Set
	uncles             *set.Set
	remove             *set.Set
	tcount             int
	ignoredTransactors *set.Set
	lowGasTransactors  *set.Set
	ownedAccounts      *set.Set
	lowGasTxs          types.Transactions
	localMinedBlocks   *uint64RingBuffer

	block *types.Block

	header   *types.Header
	txs      []*types.Transaction
	receipts []*types.Receipt
}

type worker struct {
	mu sync.Mutex

	agents    []Agent
	recv      chan *types.Block
	mux       *event.TypeMux
	quit      chan struct{}
	consensus consensus.BHU

	bhu     core.Backend
	chain   *core.ChainManager
	proc    *core.BlockProcessor
	extraDb common.Database

	coinbase common.Address
	gasPrice *big.Int
	extra    []byte

	currentMu sync.Mutex
	current   *environment

	uncleMu        sync.Mutex
	possibleUncles map[common.Hash]*types.Block

	txQueueMu sync.Mutex
	txQueue   map[common.Hash]*types.Transaction

	mining int32
	atWork int32
}

func newWorker(coinbase common.Address, bhu core.Backend) *worker {
	worker := &worker{
		bhu:            bhu,
		mux:            bhu.EventMux(),
		extraDb:        bhu.ExtraDb(),
		recv:           make(chan *types.Block),
		gasPrice:       new(big.Int),
		chain:          bhu.ChainManager(),
		proc:           bhu.BlockProcessor(),
		possibleUncles: make(map[common.Hash]*types.Block),
		coinbase:       coinbase,
		txQueue:        make(map[common.Hash]*types.Transaction),
		quit:           make(chan struct{}),
	}
	go worker.update()
	go worker.wait()

	worker.commitNewWork()

	return worker
}

func (self *worker) setBHUbase(addr common.Address) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.coinbase = addr
}

func (self *worker) pendingState() *state.StateDB {
	self.currentMu.Lock()
	defer self.currentMu.Unlock()
	return self.current.state
}

func (self *worker) pendingBlock() *types.Block {
	self.currentMu.Lock()
	defer self.currentMu.Unlock()

	if atomic.LoadInt32(&self.mining) == 0 {
		return types.NewBlock(
			self.current.header,
			self.current.txs,
			nil,
			self.current.receipts,
		)
	}
	return self.current.block
}

func (self *worker) start() {
	self.mu.Lock()
	defer self.mu.Unlock()

	atomic.StoreInt32(&self.mining, 1)

	for _, agent := range self.agents {
		agent.Start()
	}
}

func (self *worker) stop() {
	self.mu.Lock()
	defer self.mu.Unlock()

	if atomic.LoadInt32(&self.mining) == 1 {
		var keep []Agent

		for _, agent := range self.agents {
			agent.Stop()

			if _, ok := agent.(*CpuAgent); !ok {
				keep = append(keep, agent)
			}
		}
		self.agents = keep
	}

	atomic.StoreInt32(&self.mining, 0)
	atomic.StoreInt32(&self.atWork, 0)
}

func (self *worker) register(agent Agent) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.agents = append(self.agents, agent)
	agent.SetReturnCh(self.recv)
}

func (self *worker) update() {
	events := self.mux.Subscribe(core.ChainHeadEvent{}, core.ChainSideEvent{}, core.TxPreEvent{})

out:
	for {
		select {
		case event := <-events.Chan():
			switch ev := event.(type) {
			case core.ChainHeadEvent:
				self.commitNewWork()
			case core.ChainSideEvent:
				self.uncleMu.Lock()
				self.possibleUncles[ev.Block.Hash()] = ev.Block
				self.uncleMu.Unlock()
			case core.TxPreEvent:

				if atomic.LoadInt32(&self.mining) == 0 {
					self.currentMu.Lock()
					self.current.commitTransactions(types.Transactions{ev.Tx}, self.gasPrice, self.proc)
					self.currentMu.Unlock()
				}
			}
		case <-self.quit:
			break out
		}
	}

	events.Unsubscribe()
}

func newLocalMinedBlock(blockNumber uint64, prbhuvminedBlocks *uint64RingBuffer) (minedBlocks *uint64RingBuffer) {
	if prbhuvminedBlocks == nil {
		minedBlocks = &uint64RingBuffer{next: 0, ints: make([]uint64, miningLogAtDepth+1)}
	} else {
		minedBlocks = prbhuvminedBlocks
	}

	minedBlocks.ints[minedBlocks.next] = blockNumber
	minedBlocks.next = (minedBlocks.next + 1) % len(minedBlocks.ints)
	return minedBlocks
}

func (self *worker) wait() {
	for {
		for block := range self.recv {
			atomic.AddInt32(&self.atWork, -1)

			if block == nil {
				continue
			}

			parent := self.chain.GetBlock(block.ParentHash())
			if parent == nil {
				glog.V(logger.Error).Infoln("Invalid block found during mining")
				continue
			}
			if err := core.ValidateHeader(self.bhu.BlockProcessor().BHU, block.Header(), parent, true); err != nil && err != core.BlockFutureErr {
				glog.V(logger.Error).Infoln("Invalid header on mined block:", err)
				continue
			}

			stat, err := self.chain.WriteBlock(block, false)
			if err != nil {
				glog.V(logger.Error).Infoln("error writing block to chain", err)
				continue
			}

			if stat == core.CanonStatTy {

				core.PutTransactions(self.extraDb, block, block.Transactions())

				core.PutReceipts(self.extraDb, self.current.receipts)
			}

			var stale, confirm string
			canonBlock := self.chain.GetBlockByNumber(block.NumberU64())
			if canonBlock != nil && canonBlock.Hash() != block.Hash() {
				stale = "stale "
			} else {
				confirm = "Wait 5 blocks for confirmation"
				self.current.localMinedBlocks = newLocalMinedBlock(block.Number().Uint64(), self.current.localMinedBlocks)
			}

			glog.V(logger.Info).Infof("🔨  Mined %sblock (#%v / %x). %s", stale, block.Number(), block.Hash().Bytes()[:4], confirm)

			go func(block *types.Block, logs state.Logs) {
				self.mux.Post(core.NewMinedBlockEvent{block})
				self.mux.Post(core.ChainEvent{block, block.Hash(), logs})
				if stat == core.CanonStatTy {
					self.mux.Post(core.ChainHeadEvent{block})
					self.mux.Post(logs)
				}
			}(block, self.current.state.Logs())

			self.commitNewWork()
		}
	}
}

func (self *worker) push() {
	if atomic.LoadInt32(&self.mining) == 1 {
		if core.Canary(self.current.state) {
			glog.Infoln("Toxicity levels rising to deadly levels. Your canary has died. You can go back or continue down the mineshaft --more--")
			glog.Infoln("You turn back and abort mining")
			return
		}

		for _, agent := range self.agents {
			atomic.AddInt32(&self.atWork, 1)

			if agent.Work() != nil {
				agent.Work() <- self.current.block
			}
		}
	}
}

func (self *worker) makeCurrent(parent *types.Block, header *types.Header) {
	state := state.New(parent.Root(), self.bhu.StateDb())
	current := &environment{
		state:     state,
		ancestors: set.New(),
		family:    set.New(),
		uncles:    set.New(),
		header:    header,
		coinbase:  state.GetOrNewStateObject(self.coinbase),
	}

	for _, ancestor := range self.chain.GetBlocksFromHash(parent.Hash(), 7) {
		for _, uncle := range ancestor.Uncles() {
			current.family.Add(uncle.Hash())
		}
		current.family.Add(ancestor.Hash())
		current.ancestors.Add(ancestor.Hash())
	}
	accounts, _ := self.bhu.AccountManager().Accounts()

	current.remove = set.New()
	current.tcount = 0
	current.ignoredTransactors = set.New()
	current.lowGasTransactors = set.New()
	current.ownedAccounts = accountAddressesSet(accounts)
	if self.current != nil {
		current.localMinedBlocks = self.current.localMinedBlocks
	}
	self.current = current
}

func (w *worker) setGasPrice(p *big.Int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	const pct = int64(90)
	w.gasPrice = gasprice(p, pct)

	w.mux.Post(core.GasPriceChanged{w.gasPrice})
}

func (self *worker) isBlockLocallyMined(deepBlockNum uint64) bool {

	var isLocal = false
	for idx, blockNum := range self.current.localMinedBlocks.ints {
		if deepBlockNum == blockNum {
			isLocal = true
			self.current.localMinedBlocks.ints[idx] = 0
			break
		}
	}

	if !isLocal {
		return false
	}

	var block = self.chain.GetBlockByNumber(deepBlockNum)
	return block != nil && block.Coinbase() == self.coinbase
}

func (self *worker) logLocalMinedBlocks(previous *environment) {
	if previous != nil && self.current.localMinedBlocks != nil {
		nextBlockNum := self.current.block.NumberU64()
		for checkBlockNum := previous.block.NumberU64(); checkBlockNum < nextBlockNum; checkBlockNum++ {
			inspectBlockNum := checkBlockNum - miningLogAtDepth
			if self.isBlockLocallyMined(inspectBlockNum) {
				glog.V(logger.Info).Infof("🔨 🔗  Mined %d blocks back: block #%v", miningLogAtDepth, inspectBlockNum)
			}
		}
	}
}

func (self *worker) commitNewWork() {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.uncleMu.Lock()
	defer self.uncleMu.Unlock()
	self.currentMu.Lock()
	defer self.currentMu.Unlock()

	tstart := time.Now()
	parent := self.chain.CurrentBlock()
	tstamp := tstart.Unix()
	if tstamp <= int64(parent.Time()) {
		tstamp = int64(parent.Time()) + 1
	}

	if now := time.Now().Unix(); tstamp > now+4 {
		wait := time.Duration(tstamp-now) * time.Second
		glog.V(logger.Info).Infoln("We are too far in the future. Waiting for", wait)
		time.Sleep(wait)
	}

	num := parent.Number()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		Difficulty: core.CalcDifficulty(uint64(tstamp), parent.Time(), parent.Difficulty()),
		GasLimit:   core.CalcGasLimit(parent),
		GasUsed:    new(big.Int),
		Coinbase:   self.coinbase,
		Extra:      self.extra,
		Time:       uint64(tstamp),
	}

	previous := self.current
	self.makeCurrent(parent, header)
	current := self.current

	transactions := self.bhu.TxPool().GetTransactions()
	sort.Sort(types.TxByNonce{transactions})
	current.coinbase.SetGasLimit(header.GasLimit)
	current.commitTransactions(transactions, self.gasPrice, self.proc)
	self.bhu.TxPool().RemoveTransactions(current.lowGasTxs)

	var (
		uncles    []*types.Header
		badUncles []common.Hash
	)
	for hash, uncle := range self.possibleUncles {
		if len(uncles) == 2 {
			break
		}
		if err := self.commitUncle(uncle.Header()); err != nil {
			if glog.V(logger.Ridiculousness) {
				glog.V(logger.Detail).Infof("Bad uncle found and will be removed (%x)\n", hash[:4])
				glog.V(logger.Detail).Infoln(uncle)
			}
			badUncles = append(badUncles, hash)
		} else {
			glog.V(logger.Debug).Infof("commiting %x as uncle\n", hash[:4])
			uncles = append(uncles, uncle.Header())
		}
	}
	for _, hash := range badUncles {
		delete(self.possibleUncles, hash)
	}

	if atomic.LoadInt32(&self.mining) == 1 {

		core.AccumulateRewards(self.current.state, header, uncles)
		current.state.SyncObjects()
		self.current.state.Sync()
		header.Root = current.state.Root()
	}

	current.block = types.NewBlock(header, current.txs, uncles, current.receipts)
	self.current.block.Td = new(big.Int).Set(core.CalcTD(self.current.block, self.chain.GetBlock(self.current.block.ParentHash())))

	if atomic.LoadInt32(&self.mining) == 1 {
		glog.V(logger.Info).Infof("commit new work on block %v with %d txs & %d uncles. Took %v\n", current.block.Number(), current.tcount, len(uncles), time.Since(tstart))
		self.logLocalMinedBlocks(previous)
	}

	self.push()
}

func (self *worker) commitUncle(uncle *types.Header) error {
	hash := uncle.Hash()
	if self.current.uncles.Has(hash) {
		return core.UncleError("Uncle not unique")
	}
	if !self.current.ancestors.Has(uncle.ParentHash) {
		return core.UncleError(fmt.Sprintf("Uncle's parent unknown (%x)", uncle.ParentHash[0:4]))
	}
	if self.current.family.Has(hash) {
		return core.UncleError(fmt.Sprintf("Uncle already in family (%x)", hash))
	}
	self.current.uncles.Add(uncle.Hash())
	return nil
}

func (env *environment) commitTransactions(transactions types.Transactions, gasPrice *big.Int, proc *core.BlockProcessor) {
	for _, tx := range transactions {

		from, _ := tx.From()

		if tx.GasPrice().Cmp(gasPrice) < 0 && !env.ownedAccounts.Has(from) {

			env.lowGasTransactors.Add(from)

			glog.V(logger.Info).Infof("transaction(%x) below gas price (tx=%v ask=%v). All sequential txs from this address(%x) will be ignored\n", tx.Hash().Bytes()[:4], common.CurrencyToString(tx.GasPrice()), common.CurrencyToString(gasPrice), from[:4])
		}

		if env.lowGasTransactors.Has(from) {

			if !env.ownedAccounts.Has(from) {
				env.lowGasTxs = append(env.lowGasTxs, tx)
			}
			continue
		}

		if env.ignoredTransactors.Has(from) {
			continue
		}

		env.state.StartRecord(tx.Hash(), common.Hash{}, 0)

		err := env.commitTransaction(tx, proc)
		switch {
		case state.IsGasLimitErr(err):

			env.ignoredTransactors.Add(from)

			glog.V(logger.Detail).Infof("Gas limit reached for (%x) in this block. Continue to try smaller txs\n", from[:4])
		case err != nil:
			env.remove.Add(tx.Hash())

			if glog.V(logger.Detail) {
				glog.Infof("TX (%x) failed, will be removed: %v\n", tx.Hash().Bytes()[:4], err)
			}
		default:
			env.tcount++
		}
	}
}

func (env *environment) commitTransaction(tx *types.Transaction, proc *core.BlockProcessor) error {
	snap := env.state.Copy()
	receipt, _, err := proc.ApplyTransaction(env.coinbase, env.state, env.header, tx, env.header.GasUsed, true)
	if err != nil {
		env.state.Set(snap)
		return err
	}
	env.txs = append(env.txs, tx)
	env.receipts = append(env.receipts, receipt)
	return nil
}

func (self *worker) HashRate() int64 {
	return 0
}

func gasprice(price *big.Int, pct int64) *big.Int {
	p := new(big.Int).Set(price)
	p.Div(p, big.NewInt(100))
	p.Mul(p, big.NewInt(pct))
	return p
}

func accountAddressesSet(accounts []accounts.Account) *set.Set {
	accountSet := set.New()
	for _, account := range accounts {
		accountSet.Add(account.Address)
	}
	return accountSet
}
