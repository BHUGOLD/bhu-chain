package state

import (
	"bytes"
	"math/big"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"github.com/bhu/bhu-chain/trie"
)

type StateDB struct {
	db   common.Database
	trie *trie.SecureTrie
	root common.Hash

	stateObjects map[string]*StateObject

	refund *big.Int

	thash, bhash common.Hash
	txIndex      int
	logs         map[common.Hash]Logs
}

func New(root common.Hash, db common.Database) *StateDB {
	trie := trie.NewSecure(root[:], db)
	return &StateDB{root: root, db: db, trie: trie, stateObjects: make(map[string]*StateObject), refund: new(big.Int), logs: make(map[common.Hash]Logs)}
}

func (self *StateDB) PrintRoot() {
	self.trie.Trie.PrintRoot()
}

func (self *StateDB) StartRecord(thash, bhash common.Hash, ti int) {
	self.thash = thash
	self.bhash = bhash
	self.txIndex = ti
}

func (self *StateDB) AddLog(log *Log) {
	log.TxHash = self.thash
	log.BlockHash = self.bhash
	log.TxIndex = uint(self.txIndex)
	self.logs[self.thash] = append(self.logs[self.thash], log)
}

func (self *StateDB) GetLogs(hash common.Hash) Logs {
	return self.logs[hash]
}

func (self *StateDB) Logs() Logs {
	var logs Logs
	for _, lgs := range self.logs {
		logs = append(logs, lgs...)
	}
	return logs
}

func (self *StateDB) Refund(gas *big.Int) {
	self.refund.Add(self.refund, gas)
}

func (self *StateDB) HasAccount(addr common.Address) bool {
	return self.GetStateObject(addr) != nil
}

func (self *StateDB) GetBalance(addr common.Address) *big.Int {
	stateObject := self.GetStateObject(addr)
	if stateObject != nil {
		return stateObject.balance
	}

	return common.Big0
}

func (self *StateDB) GetNonce(addr common.Address) uint64 {
	stateObject := self.GetStateObject(addr)
	if stateObject != nil {
		return stateObject.nonce
	}

	return 0
}

func (self *StateDB) GetCode(addr common.Address) []byte {
	stateObject := self.GetStateObject(addr)
	if stateObject != nil {
		return stateObject.code
	}

	return nil
}

func (self *StateDB) GetState(a common.Address, b common.Hash) common.Hash {
	stateObject := self.GetStateObject(a)
	if stateObject != nil {
		return stateObject.GetState(b)
	}

	return common.Hash{}
}

func (self *StateDB) IsDeleted(addr common.Address) bool {
	stateObject := self.GetStateObject(addr)
	if stateObject != nil {
		return stateObject.remove
	}
	return false
}

func (self *StateDB) AddBalance(addr common.Address, amount *big.Int) {
	stateObject := self.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.AddBalance(amount)
	}
}

func (self *StateDB) SetNonce(addr common.Address, nonce uint64) {
	stateObject := self.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetNonce(nonce)
	}
}

func (self *StateDB) SetCode(addr common.Address, code []byte) {
	stateObject := self.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetCode(code)
	}
}

func (self *StateDB) SetState(addr common.Address, key common.Hash, value common.Hash) {
	stateObject := self.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetState(key, value)
	}
}

func (self *StateDB) Delete(addr common.Address) bool {
	stateObject := self.GetStateObject(addr)
	if stateObject != nil {
		stateObject.MarkForDeletion()
		stateObject.balance = new(big.Int)

		return true
	}

	return false
}

func (self *StateDB) UpdateStateObject(stateObject *StateObject) {

	if len(stateObject.CodeHash()) > 0 {
		self.db.Put(stateObject.CodeHash(), stateObject.code)
	}

	addr := stateObject.Address()
	self.trie.Update(addr[:], stateObject.Encode())
}

func (self *StateDB) DeleteStateObject(stateObject *StateObject) {
	addr := stateObject.Address()
	self.trie.Delete(addr[:])

}

func (self *StateDB) GetStateObject(addr common.Address) *StateObject {

	stateObject := self.stateObjects[addr.Str()]
	if stateObject != nil {
		return stateObject
	}

	data := self.trie.Get(addr[:])
	if len(data) == 0 {
		return nil
	}

	stateObject = NewStateObjectFromBytes(addr, []byte(data), self.db)
	self.SetStateObject(stateObject)

	return stateObject
}

func (self *StateDB) SetStateObject(object *StateObject) {
	self.stateObjects[object.Address().Str()] = object
}

func (self *StateDB) GetOrNewStateObject(addr common.Address) *StateObject {
	stateObject := self.GetStateObject(addr)
	if stateObject == nil {
		stateObject = self.CreateAccount(addr)
	}

	return stateObject
}

func (self *StateDB) newStateObject(addr common.Address) *StateObject {
	if glog.V(logger.Core) {
		glog.Infof("(+) %x\n", addr)
	}

	stateObject := NewStateObject(addr, self.db)
	self.stateObjects[addr.Str()] = stateObject

	return stateObject
}

func (self *StateDB) CreateAccount(addr common.Address) *StateObject {

	so := self.GetStateObject(addr)

	newSo := self.newStateObject(addr)

	if so != nil {
		newSo.balance = so.balance
	}

	return newSo
}

func (s *StateDB) Cmp(other *StateDB) bool {
	return bytes.Equal(s.trie.Root(), other.trie.Root())
}

func (self *StateDB) Copy() *StateDB {
	state := New(common.Hash{}, self.db)
	state.trie = self.trie
	for k, stateObject := range self.stateObjects {
		state.stateObjects[k] = stateObject.Copy()
	}

	state.refund.Set(self.refund)

	for hash, logs := range self.logs {
		state.logs[hash] = make(Logs, len(logs))
		copy(state.logs[hash], logs)
	}

	return state
}

func (self *StateDB) Set(state *StateDB) {
	self.trie = state.trie
	self.stateObjects = state.stateObjects

	self.refund = state.refund
	self.logs = state.logs
}

func (s *StateDB) Root() common.Hash {
	return common.BytesToHash(s.trie.Root())
}

func (s *StateDB) Trie() *trie.SecureTrie {
	return s.trie
}

func (s *StateDB) Reset() {
	s.trie.Reset()

	for _, stateObject := range s.stateObjects {
		stateObject.Reset()
	}

	s.Empty()
}

func (s *StateDB) Sync() {

	for _, stateObject := range s.stateObjects {
		stateObject.trie.Commit()
	}

	s.trie.Commit()

	s.Empty()
}

func (self *StateDB) Empty() {
	self.stateObjects = make(map[string]*StateObject)
	self.refund = new(big.Int)
}

func (self *StateDB) Refunds() *big.Int {
	return self.refund
}

func (self *StateDB) SyncIntermediate() {
	self.refund = new(big.Int)

	for _, stateObject := range self.stateObjects {
		if stateObject.dirty {
			if stateObject.remove {
				self.DeleteStateObject(stateObject)
			} else {
				stateObject.Update()

				self.UpdateStateObject(stateObject)
			}
			stateObject.dirty = false
		}
	}
}

func (self *StateDB) SyncObjects() {
	self.trie = trie.NewSecure(self.root[:], self.db)

	self.refund = new(big.Int)

	for _, stateObject := range self.stateObjects {
		if stateObject.remove {
			self.DeleteStateObject(stateObject)
		} else {
			stateObject.Update()

			self.UpdateStateObject(stateObject)
		}
		stateObject.dirty = false
	}
}

func (self *StateDB) CreateOutputForDiff() {
	for _, stateObject := range self.stateObjects {
		stateObject.CreateOutputForDiff()
	}
}
