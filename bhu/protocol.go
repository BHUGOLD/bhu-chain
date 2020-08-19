package bhu

import (
	"math/big"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/core/types"
)

var ProtocolVersions = []uint{61, 60}

var ProtocolLengths = []uint64{9, 8}

const (
	NetworkId          = 1
	ProtocolMaxMsgSize = 10 * 1024 * 1024
)

const (
	StatusMsg = iota
	NewBlockHashesMsg
	TxMsg
	GetBlockHashesMsg
	BlockHashesMsg
	GetBlocksMsg
	BlocksMsg
	NewBlockMsg
	GetBlockHashesFromNumberMsg
)

type errCode int

const (
	ErrMsgTooLarge = iota
	ErrDecode
	ErrInvalidMsgCode
	ErrProtocolVersionMismatch
	ErrNetworkIdMismatch
	ErrGenesisBlockMismatch
	ErrNoStatusMsg
	ErrExtraStatusMsg
	ErrSuspendedPeer
)

func (e errCode) String() string {
	return errorToString[int(e)]
}

var errorToString = map[int]string{
	ErrMsgTooLarge:             "Message too long",
	ErrDecode:                  "Invalid message",
	ErrInvalidMsgCode:          "Invalid message code",
	ErrProtocolVersionMismatch: "Protocol version mismatch",
	ErrNetworkIdMismatch:       "NetworkId mismatch",
	ErrGenesisBlockMismatch:    "Genesis block mismatch",
	ErrNoStatusMsg:             "No status message",
	ErrExtraStatusMsg:          "Extra status message",
	ErrSuspendedPeer:           "Suspended peer",
}

type txPool interface {
	AddTransactions([]*types.Transaction)

	GetTransactions() types.Transactions
}

type chainManager interface {
	GetBlockHashesFromHash(hash common.Hash, amount uint64) (hashes []common.Hash)
	GetBlock(hash common.Hash) (block *types.Block)
	Status() (td *big.Int, currentBlock common.Hash, genesisBlock common.Hash)
}

type statusData struct {
	ProtocolVersion uint32
	NetworkId       uint32
	TD              *big.Int
	CurrentBlock    common.Hash
	GenesisBlock    common.Hash
}

type getBlockHashesData struct {
	Hash   common.Hash
	Amount uint64
}

type getBlockHashesFromNumberData struct {
	Number uint64
	Amount uint64
}

type newBlockData struct {
	Block *types.Block
	TD    *big.Int
}
