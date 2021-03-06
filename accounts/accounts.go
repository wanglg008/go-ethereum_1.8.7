// Copyright 2017 The go-ethereum Authors
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

// Package accounts implements high level Ethereum account management.
package accounts
//accounts包实现了以太坊客户端的钱包和账户管理。以太坊的钱包提供了keyStore模式和usb两种钱包。同时以太坊的合约的ABI的代码也放在了account/abi目录。
import (
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Account represents an Ethereum account located at a specific location defined
// by the optional URL field.
type Account struct {						// 账号是20个字节的数据。 URL是可选的字段。
	Address common.Address `json:"address"` // Ethereum account address derived from the key
	URL     URL            `json:"url"`     // Optional resource locator within a backend
}

// Wallet represents a software or hardware wallet that might contain one or more 钱包又有所谓的分层确定性钱包和普通钱包。
// accounts (derived from the same seed).
type Wallet interface {						// Wallet是指包含了一个或多个账户的软件钱包或者硬件钱包
	// URL retrieves the canonical path under which this wallet is reachable. It is
	// user by upper layers to define a sorting order over all wallets from multiple
	// backends.
	URL() URL 					//URL用来获取钱包可以访问的规范路径。它会被上层使用用来从所有的后端的钱包来排序。

	// Status returns a textual status to aid the user in the current state of the
	// wallet. It also returns an error indicating any failure the wallet might have
	// encountered.
	Status() (string, error)	//用来返回一个文本值用来标识当前钱包的状态。 同时也会返回一个error用来标识钱包遇到的任何错误。

	// Open initializes access to a wallet instance. It is not meant to unlock or  Open初始化对钱包实例的访问。
	// decrypt account keys, rather simply to establish a connection to hardware   这个方法并不意味着解锁或者解密账户，而是
	// wallets and/or to access derivation seeds.                                  简单地建立与硬件钱包的连接和/或访问衍生种子。
	//
	// The passphrase parameter may or may not be used by the implementation of a  passphrase参数可能在某些实现中并不需要。
	// particular wallet instance. The reason there is no passwordless open method 没有提供一个无passphrase参数的Open方
	// is to strive towards a uniform wallet handling, oblivious to the different  法的原因是为了提供一个统一的接口。
	// backend providers.
	//
	// Please note, if you open a wallet, you must close it to release any allocated 注意，如果open一个钱包，必须close它。否则有些资源可能
	// resources (especially important when working with hardware wallets).          没有释放。 特别是使用硬件钱包的时候需要特别注意。
	Open(passphrase string) error

	// Close releases any resources held by an open wallet instance.				Close释放由Open方法占用的任何资源。
	Close() error

	// Accounts retrieves the list of signing accounts the wallet is currently aware Accounts用来获取钱包发现账户列表。对于分层次的钱包,
	// of. For hierarchical deterministic wallets, the list will not be exhaustive,  这个列表不会详尽的列出所有的账号，而是只包含在帐户派
	// rather only contain the accounts explicitly pinned during account derivation. 生期间明确固定的帐户。
	Accounts() []Account

	// Contains returns whether an account is part of this particular wallet or not.
	Contains(account Account) bool													  //Contains返回一个账号是否属于本钱包。

	// Derive attempts to explicitly derive a hierarchical deterministic account at   Derive尝试在指定的派生路径上显式派生
	// the specified derivation path. If requested, the derived account will be added 出分层确定性帐户。如果pin为true，派生
	// to the wallet's tracked account list.                                          帐户将被添加到钱包的跟踪帐户列表中。
	Derive(path DerivationPath, pin bool) (Account, error)

	// SelfDerive sets a base account derivation path from which the wallet attempts  SelfDerive设置一个基本帐户导出路径，从中钱包尝试
	// to discover non zero accounts and automatically add them to list of tracked    发现非零帐户，并自动将其添加到跟踪帐户列表中。
	// accounts.
	//
	// Note, self derivaton will increment the last component of the specified path   注意，SelfDerive将递增指定路径的最后一个组件，
	// opposed to decending into a child path to allow discovering accounts starting  而不是下降到子路径，以允许从非零组件开始发现帐户。
	// from non zero components.
	//
	// You can disable automatic account discovery by calling SelfDerive with a nil   可以通过传递一个nil的ChainStateReader来禁用自动账号发现。
	// chain state reader.
	SelfDerive(base DerivationPath, chain ethereum.ChainStateReader)

	// SignHash requests the wallet to sign the given hash.                              SignHash请求钱包来给传入的hash进行签名。
	//
	// It looks up the account specified either solely via its address contained within, 可以通过其中包含的地址（或可选地借助嵌入式URL
	// or optionally with the aid of any location metadata from the embedded URL field.  字段中的任何位置元数据）来查找指定的帐户。
	//
	// If the wallet requires additional authentication to sign the request (e.g.       如果钱包需要额外的验证才能签名(比如说需要密码来解锁账号，
	// a password to decrypt the account, or a PIN code o verify the transaction),      或者是需要一个PIN代码来验证交易。)会返回一个AuthNeededError
	// an AuthNeededError instance will be returned, containing infos for the user      的错误，里面包含了用户的信息，以及哪些字段或者操作需要提供。
	// about which fields or actions are needed. The user may retry by providing        用户可以通过SignHashWithPassphrase来签名或者通过其他手
	// the needed details via SignHashWithPassphrase, or by other means (e.g. unlock    段(在keystore里面解锁账号)
	// the account in a keystore).
	SignHash(account Account, hash []byte) ([]byte, error)

	// SignTx requests the wallet to sign the given transaction.                        SignTx请求钱包对指定的交易进行签名。
	//
	// It looks up the account specified either solely via its address contained within,
	// or optionally with the aid of any location metadata from the embedded URL field.
	//
	// If the wallet requires additional authentication to sign the request (e.g.
	// a password to decrypt the account, or a PIN code o verify the transaction),
	// an AuthNeededError instance will be returned, containing infos for the user
	// about which fields or actions are needed. The user may retry by providing
	// the needed details via SignTxWithPassphrase, or by other means (e.g. unlock
	// the account in a keystore).
	SignTx(account Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)

	// SignHashWithPassphrase requests the wallet to sign the given hash with the      SignHashWithPassphrase请求钱包使用给定的passphrase
	// given passphrase as extra authentication information.                           来签名给定的hash
	//
	// It looks up the account specified either solely via its address contained within,
	// or optionally with the aid of any location metadata from the embedded URL field.
	SignHashWithPassphrase(account Account, passphrase string, hash []byte) ([]byte, error)

	// SignTxWithPassphrase requests the wallet to sign the given transaction, with the  SignHashWithPassphrase请求钱包使用给定的passphrase
	// given passphrase as extra authentication information.                             来签名给定的transaction
	//
	// It looks up the account specified either solely via its address contained within,
	// or optionally with the aid of any location metadata from the embedded URL field.
	SignTxWithPassphrase(account Account, passphrase string, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
}

// Backend is a "wallet provider" that may contain a batch of accounts they can        Backend是一个钱包提供器,可以包含一批账号。
// sign transactions with and upon request, do so.                                     他们可以根据请求签署交易。
type Backend interface {
	// Wallets retrieves the list of wallets the backend is currently aware of.	       Wallets获取当前能够查找到的钱包
	//
	// The returned wallets are not opened by default. For software HD wallets this    返回的钱包默认是没有打开的。
	// means that no base seeds are decrypted, and for hardware wallets that no actual
	// connection is established.
	//
	// The resulting wallet list will be sorted alphabetically based on its internal   所产生的钱包列表将根据后端分配的内部URL按字母顺序排序。
	// URL assigned by the backend. Since wallets (especially hardware) may come and   由于钱包（特别是硬件钱包）可能会打开和关闭，所以在随后
	// go, the same wallet might appear at a different positions in the list during    的检索过程中，相同的钱包可能会出现在列表中的不同位置。
	// subsequent retrievals.
	Wallets() []Wallet

	// Subscribe creates an async subscription to receive notifications when the       订阅创建异步订阅，以便在后端检测到钱包的到达或离开时接收通知。
	// backend detects the arrival or departure of a wallet.
	Subscribe(sink chan<- WalletEvent) event.Subscription
}

// WalletEventType represents the different event types that can be fired by
// the wallet subscription subsystem.
type WalletEventType int

const (
	// WalletArrived is fired when a new wallet is detected either via USB or via
	// a filesystem event in the keystore.
	WalletArrived WalletEventType = iota

	// WalletOpened is fired when a wallet is successfully opened with the purpose
	// of starting any background processes such as automatic key derivation.
	WalletOpened

	// WalletDropped
	WalletDropped
)

// WalletEvent is an event fired by an account backend when a wallet arrival or
// departure is detected.
type WalletEvent struct {
	Wallet Wallet          // Wallet instance arrived or departed
	Kind   WalletEventType // Event type that happened in the system
}
