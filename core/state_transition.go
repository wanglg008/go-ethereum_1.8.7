// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"errors"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

var (
	errInsufficientBalanceForGas = errors.New("insufficient balance to pay for gas")
)

/*
The State Transitioning Model                                                                     状态转换模型

A state transition is a change made when a transaction is applied to the current world state      状态转换是指用当前的world state来执行交易，并改变当前的world state
The state transitioning model does all all the necessary work to work out a valid new state root. 状态转换做了所有所需的工作来产生一个新的有效的state root

1) Nonce handling                                             Nonce处理
2) Pre pay gas                                                预先支付Gas
3) Create a new state object if the recipient is \0*32        如果接收人是空，那么创建一个新的state object
4) Value transfer                                             转账
== If contract creation ==
  4a) Attempt to run transaction data                         尝试运行输入的数据
  4b) If valid, use result as code for the new state object   如果有效，那么用运行的结果作为新的state object的code
== end ==
5) Run Script section                                         运行脚本部分
6) Derive new state root                                      导出新的state root
*/
type StateTransition struct {
	gp         *GasPool  // 用来追踪区块内部的Gas的使用情况
	msg        Message
	gas        uint64    // 
	gasPrice   *big.Int  // gas的价格
	initialGas uint64    // 最开始的gas
	value      *big.Int  // 转账的值
	data       []byte    // 输入数据
	state      vm.StateDB
	evm        *vm.EVM   // 虚拟机
}

// Message represents a message sent to a contract.
type Message interface {
	From() common.Address
	//FromFrontier() (common.Address, error)
	To() *common.Address

	GasPrice() *big.Int
	Gas() uint64
	Value() *big.Int

	Nonce() uint64
	CheckNonce() bool
	Data() []byte
}

// IntrinsicGas computes the 'intrinsic gas' for a message with the given data.
func IntrinsicGas(data []byte, contractCreation, homestead bool) (uint64, error) {
	// Set the starting gas for the raw transaction
	var gas uint64
	if contractCreation && homestead { //因为 Gtxcreate+Gtransaction = TxGasContractCreation
		gas = params.TxGasContractCreation
	} else {
		gas = params.TxGas
	}
	// Bump the required gas by the amount of transactional data
	if len(data) > 0 {
		// Zero and non-zero bytes are priced differently
		var nz uint64
		for _, byt := range data {
			if byt != 0 {
				nz++
			}
		}
		// Make sure we don't exceed uint64 for all data combinations
		if (math.MaxUint64-gas)/params.TxDataNonZeroGas < nz {
			return 0, vm.ErrOutOfGas
		}
		gas += nz * params.TxDataNonZeroGas

		z := uint64(len(data)) - nz
		if (math.MaxUint64-gas)/params.TxDataZeroGas < z {
			return 0, vm.ErrOutOfGas
		}
		gas += z * params.TxDataZeroGas
	}
	return gas, nil
}

// NewStateTransition initialises and returns a new state transition object.
func NewStateTransition(evm *vm.EVM, msg Message, gp *GasPool) *StateTransition {
	return &StateTransition{
		gp:       gp,
		evm:      evm,
		msg:      msg,
		gasPrice: msg.GasPrice(),
		value:    msg.Value(),
		data:     msg.Data(),
		state:    evm.StateDB,
	}
}

// ApplyMessage computes the new state by applying the given message                ApplyMessage通过应用给定的Message和状态来生成新的状态
// against the old state within the environment.
//
// ApplyMessage returns the bytes returned by any EVM execution (if it took place),      ApplyMessage返回由任何EVM执行返回的字节、使用的Gas
// the gas used (which includes gas refunds) and an error if it failed. An error always  （包括Gas退款），如果失败则返回错误。错误将导致核心错误，
// indicates a core error meaning that the message would always fail for that particular 意味着这个消息对于特定的状态将总是失败,并且永远不会接受该块。
// state and would never be accepted within a block.
func ApplyMessage(evm *vm.EVM, msg Message, gp *GasPool) ([]byte, uint64, bool, error) {
	return NewStateTransition(evm, msg, gp).TransitionDb()
}

// to returns the recipient of the message.
func (st *StateTransition) to() common.Address {
	if st.msg == nil || st.msg.To() == nil /* contract creation */ {
		return common.Address{}
	}
	return *st.msg.To()
}

func (st *StateTransition) useGas(amount uint64) error {
	if st.gas < amount {
		return vm.ErrOutOfGas
	}
	st.gas -= amount

	return nil
}
//实现Gas的预扣费， 首先就扣除你的GasLimit * GasPrice的钱。 然后根据计算完的状态在退还一部分。
func (st *StateTransition) buyGas() error {
	//首先从交易的(转帐)转出方账户扣除一笔Ether，费用等于tx.data.GasLimit * tx.data.Price；同时st.initialGas = st.gas = tx.data.GasLimit；
	//然后(GasPool) gp -= st.gas。
	mgval := new(big.Int).Mul(new(big.Int).SetUint64(st.msg.Gas()), st.gasPrice)
	if st.state.GetBalance(st.msg.From()).Cmp(mgval) < 0 {
		return errInsufficientBalanceForGas
	}
	//从区块的gaspool里面减去，因为区块是由GasLimit限制整个区块的Gas使用的。 
	if err := st.gp.SubGas(st.msg.Gas()); err != nil {
		return err
	}
	st.gas += st.msg.Gas()

	st.initialGas = st.msg.Gas()
	st.state.SubBalance(st.msg.From(), mgval)//从账号里面减去GasLimit * GasPrice
	return nil
}

func (st *StateTransition) preCheck() error { //执行前的检查
	// Make sure this transaction's nonce is correct.
	if st.msg.CheckNonce() {
		nonce := st.state.GetNonce(st.msg.From())
		if nonce < st.msg.Nonce() {// 当前本地的nonce 需要和 msg的Nonce一样 不然就是状态不同步了
			return ErrNonceTooHigh
		} else if nonce > st.msg.Nonce() {
			return ErrNonceTooLow
		}
	}
	return st.buyGas()
}

// TransitionDb will transition the state by applying the current message and
// returning the result including the the used gas. It returns an error if it
// failed. An error indicates a consensus issue.
func (st *StateTransition) TransitionDb() (ret []byte, usedGas uint64, failed bool, err error) {
	if err = st.preCheck(); err != nil {  //(1)购买Gas
		return
	}
	msg := st.msg
	sender := vm.AccountRef(msg.From())
	homestead := st.evm.ChainConfig().IsHomestead(st.evm.BlockNumber)
	contractCreation := msg.To() == nil   //如果msg.To是nil,那么认为是一个合约创建

	// Pay intrinsic gas      计算最开始的Gas  g0
	gas, err := IntrinsicGas(st.data, contractCreation, homestead)
	if err != nil {
		return nil, 0, false, err
	}
	//(2)计算tx的固有Gas消耗 - intrinsicGas
	if err = st.useGas(gas); err != nil {
		return nil, 0, false, err
	}

	var (
		evm = st.evm
		// vm errors do not effect consensus and are therefor
		// not assigned to err, except for insufficient balance
		// error.
		vmerr error
	)
	//EVM执行。如果交易的(转帐)转入方地址(tx.data.Recipient)为空，调用EVM的Create()函数；否则，调用Call()函数。无论哪个函数返回后，更新st.gas。
	if contractCreation {                                   //如果是合约创建，那么调用evm的Create方法
		ret, _, st.gas, vmerr = evm.Create(sender, st.data, st.gas, st.value)
	} else {
		// Increment the nonce for the next transaction     //如果是方法调用。那么首先设置sender的nonce。
		st.state.SetNonce(msg.From(), st.state.GetNonce(sender.Address())+1)
		ret, st.gas, vmerr = evm.Call(sender, st.to(), st.data, st.gas, st.value)
	}
	if vmerr != nil {
		log.Debug("VM returned with error", "err", vmerr)
		// The only possible consensus-error would be if there wasn't
		// sufficient balance to make the transfer happen. The first
		// balance transfer may never fail.
		if vmerr == vm.ErrInsufficientBalance {
			return nil, 0, false, vmerr
		}
	}
	st.refundGas() 			//偿退Gas
	st.state.AddBalance(st.evm.Coinbase, new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), st.gasPrice)) //奖励所属区块的挖掘者
	//requiredGas和gasUsed的区别一个是没有退税的， 一个是退税了的。
	return ret, st.gasUsed(), vmerr != nil, err
}
//退税是为了奖励大家运行一些能够减轻区块链负担的指令， 比如清空账户的storage. 或者是运行suicide命令来清空账号。
func (st *StateTransition) refundGas() {
	// Apply refund counter, capped to half of the used gas.
	// 首先将剩余st.gas折算成Ether，归还给交易的(转帐)转出方账户
	refund := st.gasUsed() / 2
	if refund > st.state.GetRefund() { //然后基于实际消耗量requiredGas，系统提供一定的补偿，数量为refundGas。
		refund = st.state.GetRefund()
	}
	st.gas += refund

	// Return ETH for remaining gas, exchanged at the original rate.
	remaining := new(big.Int).Mul(new(big.Int).SetUint64(st.gas), st.gasPrice)
	st.state.AddBalance(st.msg.From(), remaining)// 首先把用户还剩下的Gas还回去

	// Also return remaining gas to the block gas counter so it is   refundGas 所折算的Ether会被立即加在(转帐)转出方账户上，同时st.gas += refundGas，
	// available for the next transaction.                           gp += st.gas，即剩余的Gas加上系统补偿的Gas，被一起归并进GasPool，供之后的交易执行使用。
	st.gp.AddGas(st.gas)// 同时也把退税的钱还给gaspool给下个交易腾点Gas空间。
}

// gasUsed returns the amount of gas used up by the state transition.
func (st *StateTransition) gasUsed() uint64 {
	return st.initialGas - st.gas
}
