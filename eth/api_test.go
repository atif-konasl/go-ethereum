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

package eth

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
)

var dumper = spew.ConfigState{Indent: "    "}

func accountRangeTest(t *testing.T, trie *state.Trie, statedb *state.StateDB, start common.Hash, requestedNum int, expectedNum int) state.IteratorDump {
	result := statedb.IteratorDump(true, true, false, start.Bytes(), requestedNum)

	if len(result.Accounts) != expectedNum {
		t.Fatalf("expected %d results, got %d", expectedNum, len(result.Accounts))
	}
	for address := range result.Accounts {
		if address == (common.Address{}) {
			t.Fatalf("empty address returned")
		}
		if !statedb.Exist(address) {
			t.Fatalf("account not found in state %s", address.Hex())
		}
	}
	return result
}

type resultHash []common.Hash

func (h resultHash) Len() int           { return len(h) }
func (h resultHash) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h resultHash) Less(i, j int) bool { return bytes.Compare(h[i].Bytes(), h[j].Bytes()) < 0 }

func TestAccountRange(t *testing.T) {
	t.Parallel()

	var (
		statedb  = state.NewDatabaseWithConfig(rawdb.NewMemoryDatabase(), nil)
		state, _ = state.New(common.Hash{}, statedb, nil)
		addrs    = [AccountRangeMaxResults * 2]common.Address{}
		m        = map[common.Address]bool{}
	)

	for i := range addrs {
		hash := common.HexToHash(fmt.Sprintf("%x", i))
		addr := common.BytesToAddress(crypto.Keccak256Hash(hash.Bytes()).Bytes())
		addrs[i] = addr
		state.SetBalance(addrs[i], big.NewInt(1))
		if _, ok := m[addr]; ok {
			t.Fatalf("bad")
		} else {
			m[addr] = true
		}
	}
	state.Commit(true)
	root := state.IntermediateRoot(true)

	trie, err := statedb.OpenTrie(root)
	if err != nil {
		t.Fatal(err)
	}
	accountRangeTest(t, &trie, state, common.Hash{}, AccountRangeMaxResults/2, AccountRangeMaxResults/2)
	// test pagination
	firstResult := accountRangeTest(t, &trie, state, common.Hash{}, AccountRangeMaxResults, AccountRangeMaxResults)
	secondResult := accountRangeTest(t, &trie, state, common.BytesToHash(firstResult.Next), AccountRangeMaxResults, AccountRangeMaxResults)

	hList := make(resultHash, 0)
	for addr1 := range firstResult.Accounts {
		// If address is empty, then it makes no sense to compare
		// them as they might be two different accounts.
		if addr1 == (common.Address{}) {
			continue
		}
		if _, duplicate := secondResult.Accounts[addr1]; duplicate {
			t.Fatalf("pagination test failed:  results should not overlap")
		}
		hList = append(hList, crypto.Keccak256Hash(addr1.Bytes()))
	}
	// Test to see if it's possible to recover from the middle of the previous
	// set and get an even split between the first and second sets.
	sort.Sort(hList)
	middleH := hList[AccountRangeMaxResults/2]
	middleResult := accountRangeTest(t, &trie, state, middleH, AccountRangeMaxResults, AccountRangeMaxResults)
	missing, infirst, insecond := 0, 0, 0
	for h := range middleResult.Accounts {
		if _, ok := firstResult.Accounts[h]; ok {
			infirst++
		} else if _, ok := secondResult.Accounts[h]; ok {
			insecond++
		} else {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("%d hashes in the 'middle' set were neither in the first not the second set", missing)
	}
	if infirst != AccountRangeMaxResults/2 {
		t.Fatalf("Imbalance in the number of first-test results: %d != %d", infirst, AccountRangeMaxResults/2)
	}
	if insecond != AccountRangeMaxResults/2 {
		t.Fatalf("Imbalance in the number of second-test results: %d != %d", insecond, AccountRangeMaxResults/2)
	}
}

func TestEmptyAccountRange(t *testing.T) {
	t.Parallel()

	var (
		statedb  = state.NewDatabase(rawdb.NewMemoryDatabase())
		state, _ = state.New(common.Hash{}, statedb, nil)
	)
	state.Commit(true)
	state.IntermediateRoot(true)
	results := state.IteratorDump(true, true, true, (common.Hash{}).Bytes(), AccountRangeMaxResults)
	if bytes.Equal(results.Next, (common.Hash{}).Bytes()) {
		t.Fatalf("Empty results should not return a second page")
	}
	if len(results.Accounts) != 0 {
		t.Fatalf("Empty state should not return addresses: %v", results.Accounts)
	}
}

func TestStorageRangeAt(t *testing.T) {
	t.Parallel()

	// Create a state where account 0x010000... has a few storage entries.
	var (
		state, _ = state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		addr     = common.Address{0x01}
		keys     = []common.Hash{ // hashes of Keys of storage
			common.HexToHash("340dd630ad21bf010b4e676dbfa9ba9a02175262d1fa356232cfde6cb5b47ef2"),
			common.HexToHash("426fcb404ab2d5d8e61a3d918108006bbb0a9be65e92235bb10eefbdb6dcd053"),
			common.HexToHash("48078cfed56339ea54962e72c37c7f588fc4f8e5bc173827ba75cb10a63a96a5"),
			common.HexToHash("5723d2c3a83af9b735e3b7f21531e5623d183a9095a56604ead41f3582fdfb75"),
		}
		storage = storageMap{
			keys[0]: {Key: &common.Hash{0x02}, Value: common.Hash{0x01}},
			keys[1]: {Key: &common.Hash{0x04}, Value: common.Hash{0x02}},
			keys[2]: {Key: &common.Hash{0x01}, Value: common.Hash{0x03}},
			keys[3]: {Key: &common.Hash{0x03}, Value: common.Hash{0x04}},
		}
	)
	for _, entry := range storage {
		state.SetState(addr, *entry.Key, entry.Value)
	}

	// Check a few combinations of limit and start/end.
	tests := []struct {
		start []byte
		limit int
		want  StorageRangeResult
	}{
		{
			start: []byte{}, limit: 0,
			want: StorageRangeResult{storageMap{}, &keys[0]},
		},
		{
			start: []byte{}, limit: 100,
			want: StorageRangeResult{storage, nil},
		},
		{
			start: []byte{}, limit: 2,
			want: StorageRangeResult{storageMap{keys[0]: storage[keys[0]], keys[1]: storage[keys[1]]}, &keys[2]},
		},
		{
			start: []byte{0x00}, limit: 4,
			want: StorageRangeResult{storage, nil},
		},
		{
			start: []byte{0x40}, limit: 2,
			want: StorageRangeResult{storageMap{keys[1]: storage[keys[1]], keys[2]: storage[keys[2]]}, &keys[3]},
		},
	}
	for _, test := range tests {
		result, err := storageRangeAt(state.StorageTrie(addr), test.start, test.limit)
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(result, test.want) {
			t.Fatalf("wrong result for range 0x%x.., limit %d:\ngot %s\nwant %s",
				test.start, test.limit, dumper.Sdump(result), dumper.Sdump(&test.want))
		}
	}
}

var (
	testKey, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	testAddr    = crypto.PubkeyToAddress(testKey.PublicKey)
	testBalance = big.NewInt(2e10)
)

func generateTestChain() (*core.Genesis, []*types.Block) {
	db := rawdb.NewMemoryDatabase()
	config := params.AllEthashProtocolChanges
	genesis := &core.Genesis{
		Config:    config,
		Alloc:     core.GenesisAlloc{testAddr: {Balance: testBalance}},
		ExtraData: []byte("test genesis"),
		Timestamp: 9000,
	}
	generate := func(i int, g *core.BlockGen) {
		g.OffsetTime(5)
		g.SetExtra([]byte("test"))
	}
	gblock := genesis.ToBlock(db)
	engine := ethash.NewFaker()
	blocks, _ := core.GenerateChain(config, gblock, engine, db, 10, generate)
	blocks = append([]*types.Block{gblock}, blocks...)
	return genesis, blocks
}

func generateTestChainWithFork(n int, fork int) (*core.Genesis, []*types.Block, []*types.Block) {
	if fork >= n {
		fork = n - 1
	}
	db := rawdb.NewMemoryDatabase()
	config := params.AllEthashProtocolChanges
	genesis := &core.Genesis{
		Config:    config,
		Alloc:     core.GenesisAlloc{testAddr: {Balance: testBalance}},
		ExtraData: []byte("test genesis"),
		Timestamp: 9000,
	}
	generate := func(i int, g *core.BlockGen) {
		g.OffsetTime(5)
		g.SetExtra([]byte("test"))
	}
	generateFork := func(i int, g *core.BlockGen) {
		g.OffsetTime(5)
		g.SetExtra([]byte("testF"))
	}
	gblock := genesis.ToBlock(db)
	engine := ethash.NewFaker()
	blocks, _ := core.GenerateChain(config, gblock, engine, db, n, generate)
	blocks = append([]*types.Block{gblock}, blocks...)
	forkedBlocks, _ := core.GenerateChain(config, blocks[fork], engine, db, n-fork, generateFork)
	return genesis, blocks, forkedBlocks
}

func TestEth2ProduceBlock(t *testing.T) {
	genesis, blocks := generateTestChain()

	n, err := node.New(&node.Config{})
	if err != nil {
		t.Fatalf("could not get node: %v", err)
	}
	ethservice, err := New(n, &Config{Genesis: genesis, Ethash: ethash.Config{PowMode: ethash.ModeFake}})
	if err != nil {
		t.Fatalf("can't create new ethereum service: %v", err)
	}
	if err := n.Start(); err != nil {
		t.Fatalf("can't start test node: %v", err)
	}
	if _, err := ethservice.BlockChain().InsertChain(blocks[1:9]); err != nil {
		t.Fatalf("can't import test blocks: %v", err)
	}
	ethservice.SetEtherbase(testAddr)

	api := NewEth2API(ethservice)
	signer := types.NewEIP155Signer(ethservice.BlockChain().Config().ChainID)
	tx, err := types.SignTx(types.NewTransaction(0, blocks[8].Coinbase(), big.NewInt(1000), params.TxGas, nil, nil), signer, testKey)
	ethservice.txPool.AddLocal(tx)
	blockParams := ProduceBlockParams{
		ParentRoot: blocks[8].ParentHash(),
		Slot:       blocks[8].NumberU64(),
		Timestamp:  blocks[8].Time(),
	}
	execData, err := api.ProduceBlock(blockParams)

	if err != nil {
		t.Fatalf("error producing block, err=%v", err)
	}

	if len(execData.Transactions) != 1 {
		t.Fatalf("invalid number of transactions %d != 1", len(execData.Transactions))
	}
}

func TestEth2ProduceBlockWithAnotherBlocksTxs(t *testing.T) {
	genesis, blocks := generateTestChain()

	n, err := node.New(&node.Config{})
	if err != nil {
		t.Fatalf("could not get node: %v", err)
	}
	ethservice, err := New(n, &Config{Genesis: genesis, Ethash: ethash.Config{PowMode: ethash.ModeFake}})
	if err != nil {
		t.Fatalf("can't create new ethereum service: %v", err)
	}
	if err := n.Start(); err != nil {
		t.Fatalf("can't start test node: %v", err)
	}
	if _, err := ethservice.BlockChain().InsertChain(blocks[1:9]); err != nil {
		t.Fatalf("can't import test blocks: %v", err)
	}
	ethservice.SetEtherbase(testAddr)

	api := NewEth2API(ethservice)

	// Put the 10th block's tx in the pool and produce a new block
	api.AddBlockTxs(blocks[9])
	blockParams := ProduceBlockParams{
		ParentRoot: blocks[9].ParentHash(),
		Slot:       blocks[9].NumberU64(),
		Timestamp:  blocks[9].Time(),
	}
	execData, err := api.ProduceBlock(blockParams)
	if err != nil {
		t.Fatalf("error producing block, err=%v", err)
	}

	if len(execData.Transactions) != blocks[9].Transactions().Len() {
		t.Fatalf("invalid number of transactions %d != 1", len(execData.Transactions))
	}
}

func TestEth2InsertBlock(t *testing.T) {
	genesis, blocks, forkedBlocks := generateTestChainWithFork(10, 4)

	n, err := node.New(&node.Config{})
	if err != nil {
		t.Fatalf("could not get node: %v", err)
	}
	ethservice, err := New(n, &Config{Genesis: genesis, Ethash: ethash.Config{PowMode: ethash.ModeFake}})
	if err != nil {
		t.Fatalf("can't create new ethereum service: %v", err)
	}
	if err := n.Start(); err != nil {
		t.Fatalf("can't start test node: %v", err)
	}
	if _, err := ethservice.BlockChain().InsertChain(blocks[1:5]); err != nil {
		t.Fatalf("can't import test blocks: %v", err)
	}

	}

	api := NewEth2API(ethservice)
	for i := 5; i < 10; i++ {
		p := InsertBlockParams{
			ProduceBlockParams: ProduceBlockParams{
				ParentRoot: ethservice.BlockChain().CurrentBlock().Hash(),
				Slot:       blocks[i].NumberU64(),
				Timestamp:  blocks[i].Time(),
			},
			ExecutableData: ExecutableData{
				Coinbase:     blocks[i].Coinbase(),
				StateRoot:    blocks[i].Root(),
				Difficulty:   blocks[i].Difficulty(),
				GasLimit:     blocks[i].GasLimit(),
				GasUsed:      blocks[i].GasUsed(),
				Transactions: []*types.Transaction(blocks[i].Transactions()),
				ReceiptRoot:  blocks[i].ReceiptHash(),
				LogsBloom:    blocks[i].Bloom().Bytes(),
				BlockHash:    blocks[i].Hash(),
			},
		}
		err := api.InsertBlock(p)
		if err != nil {
			t.Fatalf("Failed to insert block: %v", err)
		}
	}

	// Introduce the fork point
	lastBlockHash, lastBlockNum := blocks[4].Hash(), blocks[4].NumberU64()
	for i := 0; i < 4; i++ {
		lastBlockNum = lastBlockNum + 1
		p := InsertBlockParams{
			ProduceBlockParams: ProduceBlockParams{
				ParentRoot: lastBlockHash,
				Slot:      lastBlockNum,
				Timestamp: forkedBlocks[i].Time(),
			},
			ExecutableData: ExecutableData{
				Coinbase:     forkedBlocks[i].Coinbase(),
				StateRoot:    forkedBlocks[i].Root(),
				Difficulty:   forkedBlocks[i].Difficulty(),
				GasLimit:     forkedBlocks[i].GasLimit(),
				GasUsed:      forkedBlocks[i].GasUsed(),
				Transactions: []*types.Transaction(blocks[i].Transactions()),
				ReceiptRoot:  forkedBlocks[i].ReceiptHash(),
				LogsBloom:    forkedBlocks[i].Bloom().Bytes(),
				BlockHash:    forkedBlocks[i].Hash(),
			},
		}
		err := api.InsertBlock(p)
		if err != nil {
			t.Fatalf("Failed to insert block: %v", err)
		}
		lastBlockHash = insertBlockParamsToBlock(p).Hash()
	}

	exp := common.HexToHash("526db89301fc787799ef8c272fe512898b97ad96d0b69caee19dc5393b092110")
	if ethservice.BlockChain().CurrentBlock().Hash() != exp {
		t.Fatalf("Wrong head after inserting fork %x != %x", exp, ethservice.BlockChain().CurrentBlock().Hash())
	}
}

//func TestEth2SetHead(t *testing.T) {
//genesis, blocks, forkedBlocks := generateTestChainWithFork(10, 5)

//n, err := node.New(&node.Config{})
//if err != nil {
//t.Fatalf("could not get node: %v", err)
//}
//ethservice, err := New(n, &Config{Genesis: genesis, Ethash: ethash.Config{PowMode: ethash.ModeFake}})
//if err != nil {
//t.Fatalf("can't create new ethereum service: %v", err)
//}
//if err := n.Start(); err != nil {
//t.Fatalf("can't start test node: %v", err)
//}
//if _, err := ethservice.BlockChain().InsertChain(blocks[1:5]); err != nil {
//t.Fatalf("can't import test blocks: %v", err)
//}

//api := NewEth2API(ethservice)
//for i := 5; i < 10; i++ {
//var blockRLP bytes.Buffer
//rlp.Encode(&blockRLP, blocks[i])
//err := api.InsertBlock(blockRLP.Bytes())
//if err != nil {
//t.Fatalf("Failed to insert block: %v", err)
//}
//}
//api.head = blocks[9].Hash()

//if ethservice.BlockChain().CurrentBlock().Hash() != blocks[9].Hash() {
//t.Fatalf("Wrong head")
//}

//for i := 0; i < 3; i++ {
//var blockRLP bytes.Buffer
//rlp.Encode(&blockRLP, forkedBlocks[i])
//err := api.InsertBlock(blockRLP.Bytes())
//if err != nil {
//t.Fatalf("Failed to insert block: %v", err)
//}
//}

//api.SetHead(forkedBlocks[2].Hash())

//if ethservice.BlockChain().CurrentBlock().Hash() == forkedBlocks[2].Hash() {
//t.Fatalf("Wrong head after inserting fork %x != %x", blocks[9].Hash(), ethservice.BlockChain().CurrentBlock().Hash())
//}
//if api.head != forkedBlocks[2].Hash() {
//t.Fatalf("Registered wrong head")
//}
//}
