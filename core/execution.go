package core

import (
	"math/big"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/core/state"
	"github.com/bhu/bhu-chain/core/vm"
	"github.com/bhu/bhu-chain/crypto"
	"github.com/bhu/bhu-chain/params"
)

type Execution struct {
	env     vm.Environment
	address *common.Address
	input   []byte
	bhuvm   vm.VirtualMachine

	Gas, price, value *big.Int
}

func NewExecution(env vm.Environment, address *common.Address, input []byte, gas, gasPrice, value *big.Int) *Execution {
	exe := &Execution{env: env, address: address, input: input, Gas: gas, price: gasPrice, value: value}
	exe.bhuvm = vm.NewVm(env)
	return exe
}

func (self *Execution) Call(codeAddr common.Address, caller vm.ContextRef) ([]byte, error) {

	code := self.env.State().GetCode(codeAddr)

	return self.exec(&codeAddr, code, caller)
}

func (self *Execution) Create(caller vm.ContextRef) (ret []byte, err error, account *state.StateObject) {

	code := self.input
	self.input = nil
	ret, err = self.exec(nil, code, caller)

	if err != nil {
		return nil, err, nil
	}
	account = self.env.State().GetStateObject(*self.address)
	return
}

func (self *Execution) exec(contextAddr *common.Address, code []byte, caller vm.ContextRef) (ret []byte, err error) {
	env := self.env
	bhuvm := self.bhuvm
	if env.Depth() > int(params.CallCreateDepth.Int64()) {
		caller.ReturnGas(self.Gas, self.price)

		return nil, vm.DepthError
	}

	vsnapshot := env.State().Copy()
	var createAccount bool
	if self.address == nil {

		nonce := env.State().GetNonce(caller.Address())
		env.State().SetNonce(caller.Address(), nonce+1)

		addr := crypto.CreateAddress(caller.Address(), nonce)

		self.address = &addr
		createAccount = true
	}
	snapshot := env.State().Copy()

	var (
		from = env.State().GetStateObject(caller.Address())
		to   *state.StateObject
	)
	if createAccount {
		to = env.State().CreateAccount(*self.address)
	} else {
		to = env.State().GetOrNewStateObject(*self.address)
	}

	err = env.Transfer(from, to, self.value)
	if err != nil {
		env.State().Set(vsnapshot)

		caller.ReturnGas(self.Gas, self.price)

		return nil, ValueTransferErr("insufficient funds to transfer value. Req %v, has %v", self.value, from.Balance())
	}

	context := vm.NewContext(caller, to, self.value, self.Gas, self.price)
	context.SetCallCode(contextAddr, code)

	ret, err = bhuvm.Run(context, self.input)
	if err != nil {
		env.State().Set(snapshot)
	}

	return
}
