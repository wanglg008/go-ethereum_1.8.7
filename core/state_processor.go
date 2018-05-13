// Copyright 2015 The go-ethereum Authors
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
//StateTransition是用来处理一个一个的交易的。那么StateProcessor就是用来处理区块级别的交易的。
import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config *params.ChainConfig // Chain configuration options
	bc     *BlockChain         // Canonical block chain
	engine consensus.Engine    // Consensus engine used for block rewards
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, bc *BlockChain, engine consensus.Engine) *StateProcessor {
	return &StateProcessor{
		config: config,
		bc:     bc,
		engine: engine,
	}
}
// 执行tx的入口函数
// Process processes the state changes according to the Ethereum rules by running Process 根据以太坊规则运行交易信息来对statedb进行状
// the transaction messages using the statedb and applying any rewards to both            态改变，以及奖励挖矿者或者是其他的叔父节点。
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and             Process返回执行过程中累计的收据和日志，并返回过程中
// returns the amount of gas that was used in the process. If any of the                使用的Gas。 如果由于Gas不足而导致任何交易执行失败，
// transactions failed to execute due to insufficient gas it will return an error.      将返回错误。
func (p *StateProcessor) Process(block *types.Block, statedb *state.StateDB, cfg vm.Config) (types.Receipts, []*types.Log, uint64, error) {
	var (
		receipts types.Receipts
		usedGas  = new(uint64)
		header   = block.Header()
		allLogs  []*types.Log
		gp       = new(GasPool).AddGas(block.GasLimit())
	)
	// Mutate the the block and state according to any hard-fork specs
	if p.config.DAOForkSupport && p.config.DAOForkBlock != nil && p.config.DAOForkBlock.Cmp(block.Number()) == 0 {//DAO事件的硬分叉处理
		misc.ApplyDAOHardFork(statedb)
	}
	// Iterate over and process the individual transactions
	// 将Block里的所有tx逐个遍历执行。具体的执行函数叫ApplyTransaction()，它每次执行tx, 会返回一个收据(Receipt)对象
	for i, tx := range block.Transactions() {
		statedb.Prepare(tx.Hash(), block.Hash(), i)
		receipt, _, err := ApplyTransaction(p.config, p.bc, nil, gp, statedb, header, tx, usedGas, cfg)
		if err != nil {
			return nil, nil, 0, err
		}
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
	}
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	p.engine.Finalize(p.bc, header, statedb, block.Transactions(), block.Uncles(), receipts)

	return receipts, allLogs, *usedGas, nil // 返回收据 日志 总的Gas使用量和nil
}

// ApplyTransaction attempts to apply a transaction to the given state database ApplyTransaction尝试将事务应用于给定的状态数据库，并使用
// and uses the input parameters for its environment. It returns the receipt    其环境的输入参数。它返回事务的收据，使用的Gas和错误，如果
// for the transaction, gas used and an error if the transaction failed,        交易失败，表明块是无效的。
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, bc *BlockChain, author *common.Address, gp *GasPool, statedb *state.StateDB, header *types.Header, tx *types.Transaction, usedGas *uint64, cfg vm.Config) (*types.Receipt, uint64, error) {
	msg, err := tx.AsMessage(types.MakeSigner(config, header.Number)) //把交易转换成Message 
	if err != nil {
		return nil, 0, err
	}
	// Create a new context to be used in the EVM environment         //每一个交易都创建了新的虚拟机环境。
	context := NewEVMContext(msg, header, bc, author)                 //EVM对象
	// Create a new environment which holds all relevant information
	// about the transaction and calling mechanisms.
	vmenv := vm.NewEVM(context, statedb, config, cfg)
	// Apply the transaction to the current state (included in the env)
	_, gas, failed, err := ApplyMessage(vmenv, msg, gp)               //完成tx的执行
	if err != nil {
		return nil, 0, err
	}
	// Update the state with pending changes
	var root []byte // 求得中间状态
	if config.IsByzantium(header.Number) {
		statedb.Finalise(true)
	} else {
		root = statedb.IntermediateRoot(config.IsEIP158(header.Number)).Bytes()
	}
	*usedGas += gas

	// Create a new receipt for the transaction, storing the intermediate root and gas used by the tx
	// based on the eip phase, we're passing wether the root touch-delete accounts.
	// 创建一个收据Receipt对象，并返回该Recetip对象，以及整个tx执行过程所消耗Gas数量。
	receipt := types.NewReceipt(root, failed, *usedGas)
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = gas
	// if the transaction created a contract, store the creation address in the receipt.
	if msg.To() == nil {// 如果是创建合约的交易.那么把创建地址存储到收据里面.
		receipt.ContractAddress = crypto.CreateAddress(vmenv.Context.Origin, tx.Nonce())
	}
	// Set the receipt logs and create a bloom for filtering
	receipt.Logs = statedb.GetLogs(tx.Hash())
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})

	return receipt, gas, err // 拿到所有的日志并创建日志的布隆过滤器.
}
