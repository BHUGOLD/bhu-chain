package vm

import (
	"math/big"

	"github.com/bhu/bhu-chain/common"
)

var bigMaxUint64 = new(big.Int).SetUint64(^uint64(0))

type destinations map[common.Hash]map[uint64]struct{}

func (d destinations) has(codehash common.Hash, code []byte, dest *big.Int) bool {

	if dest.Cmp(bigMaxUint64) > 0 {
		return false
	}
	m, analysed := d[codehash]
	if !analysed {
		m = jumpdests(code)
		d[codehash] = m
	}
	_, ok := m[dest.Uint64()]
	return ok
}

func jumpdests(code []byte) map[uint64]struct{} {
	m := make(map[uint64]struct{})
	for pc := uint64(0); pc < uint64(len(code)); pc++ {
		var op OpCode = OpCode(code[pc])
		switch op {
		case PUSH1, PUSH2, PUSH3, PUSH4, PUSH5, PUSH6, PUSH7, PUSH8, PUSH9, PUSH10, PUSH11, PUSH12, PUSH13, PUSH14, PUSH15, PUSH16, PUSH17, PUSH18, PUSH19, PUSH20, PUSH21, PUSH22, PUSH23, PUSH24, PUSH25, PUSH26, PUSH27, PUSH28, PUSH29, PUSH30, PUSH31, PUSH32:
			a := uint64(op) - uint64(PUSH1) + 1
			pc += a
		case JUMPDEST:
			m[pc] = struct{}{}
		}
	}
	return m
}
