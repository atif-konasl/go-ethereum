// Copyright 2020 The go-ethereum Authors
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

package snapshot

import (
	"bytes"
	"encoding/binary"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/trie"
	"math/rand"
	"testing"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestTrieGeneration(t *testing.T) {
	// Create an empty base layer and a snapshot tree out of it
	base := &diskLayer{
		diskdb: rawdb.NewMemoryDatabase(),
		root:   common.HexToHash("0x01"),
		cache:  fastcache.New(1024 * 500),
	}
	snaps := &Tree{
		layers: map[common.Hash]snapshot{
			base.root: base,
		},
	}
	rand.Seed(1338)
	// Stack three diff layers on top with various overlaps
	snaps.Update(common.HexToHash("0x02"), common.HexToHash("0x01"), nil,
		randomAccountSet("0x11", "0x22", "0x33"), nil)
	// We call this once before the benchmark, so the creation of
	// sorted accountlists are not included in the results.
	head := snaps.Snapshot(common.HexToHash("0x02"))
	it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
	hash := generateTrieRoot(it, AppendOnlyGenerate)
	if got, exp := hash, common.HexToHash("333a7c170a3d97bd53321d0f39b1a6b9a35b286ad2d3b3ced72ca339197c5dca"); exp != got {
		t.Fatalf("expected %x got %x", exp, got)
	}
}

func TestTrieGenerationAppendonly(t *testing.T) {
	// Create an empty base layer and a snapshot tree out of it
	base := &diskLayer{
		diskdb: rawdb.NewMemoryDatabase(),
		root:   common.HexToHash("0x01"),
		cache:  fastcache.New(1024 * 500),
	}
	snaps := &Tree{
		layers: map[common.Hash]snapshot{
			base.root: base,
		},
	}
	rand.Seed(1337)
	// Stack three diff layers on top with various overlaps
	snaps.Update(common.HexToHash("0x02"), common.HexToHash("0x01"), nil,
		randomAccountSet("0x11", "0x22", "0x33"), nil)
	// We call this once before the benchmark, so the creation of
	// sorted accountlists are not included in the results.
	head := snaps.Snapshot(common.HexToHash("0x02"))
	it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
	hash := generateTrieRoot(it, AppendOnlyGenerate)
	if got, exp := hash, common.HexToHash("c9dd8a9602446bfcce27efbb0188a78761bf5473dd363f4ae2f17975a308344a"); exp != got {
		t.Fatalf("expected %x got %x", exp, got)
	}
}

func TestMultipleStackTrieInsertion(t *testing.T) {
	// Get a fairly large trie
	// Create a custom account factory to recreate the same addresses
	makeAccounts := func(num int) map[common.Hash][]byte {
		accounts := make(map[common.Hash][]byte)
		for i := 0; i < num; i++ {
			h := common.Hash{}
			binary.BigEndian.PutUint64(h[:], uint64(i+1))
			accounts[h] = randomAccountWithSmall()
		}
		return accounts
	}
	// Build up a large stack of snapshots
	base := &diskLayer{
		diskdb: rawdb.NewMemoryDatabase(),
		root:   common.HexToHash("0x01"),
		cache:  fastcache.New(1024 * 500),
	}
	snaps := &Tree{
		layers: map[common.Hash]snapshot{
			base.root: base,
		},
	}

	// 4K accounts
	snaps.Update(common.HexToHash("0x02"), common.HexToHash("0x01"), nil, makeAccounts(4000), nil)
	head := snaps.Snapshot(common.HexToHash("0x02"))
	// Call it once to make it create the lists before test starts
	head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))

	var got1 common.Hash
	it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
	got1 = generateTrieRoot(it, PruneGenerate)

	var got2 common.Hash
	it = head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
	got2 = generateTrieRoot(it, StackGenerate)
	if got2 != got1 {
		t.Fatalf("Error: got %x exp %x", got2, got1)
	}
}

// BenchmarkTrieGeneration/4K/standard-8         	     127	   9429425 ns/op	 6188077 B/op	   58026 allocs/op
// BenchmarkTrieGeneration/4K/pruning-8          	      72	  16544534 ns/op	 6617322 B/op	   55016 allocs/op
// BenchmarkTrieGeneration/4K/stack-8            	     159	   6452936 ns/op	 6308393 B/op	   12022 allocs/op
// BenchmarkTrieGeneration/10K/standard-8        	      50	  25025175 ns/op	17283703 B/op	  151023 allocs/op
// BenchmarkTrieGeneration/10K/pruning-8         	      28	  38141602 ns/op	16540254 B/op	  137520 allocs/op
// BenchmarkTrieGeneration/10K/stack-8           	      60	  18888649 ns/op	17557314 B/op	   30067 allocs/op
func BenchmarkTrieGeneration(b *testing.B) {
	// Get a fairly large trie
	// Create a custom account factory to recreate the same addresses
	makeAccounts := func(num int) map[common.Hash][]byte {
		accounts := make(map[common.Hash][]byte)
		for i := 0; i < num; i++ {
			h := common.Hash{}
			binary.BigEndian.PutUint64(h[:], uint64(i+1))
			accounts[h] = randomAccountWithSmall()
		}
		return accounts
	}
	// Build up a large stack of snapshots
	base := &diskLayer{
		diskdb: rawdb.NewMemoryDatabase(),
		root:   common.HexToHash("0x01"),
		cache:  fastcache.New(1024 * 500),
	}
	snaps := &Tree{
		layers: map[common.Hash]snapshot{
			base.root: base,
		},
	}
	b.Run("4K", func(b *testing.B) {
		// 4K accounts
		snaps.Update(common.HexToHash("0x02"), common.HexToHash("0x01"), nil, makeAccounts(4000), nil)
		head := snaps.Snapshot(common.HexToHash("0x02"))
		// Call it once to make it create the lists before test starts
		head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
		b.Run("standard", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			var got common.Hash
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				got = generateTrieRoot(it, StdGenerate)
			}
			b.StopTimer()
			if exp := common.HexToHash("fecc4e1fce05c888c8acc8baa2d7677a531714668b7a09b5ede6e3e110be266b"); got != exp {
				b.Fatalf("Error: got %x exp %x", got, exp)
			}
		})
		b.Run("pruning", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			var got common.Hash
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				got = generateTrieRoot(it, PruneGenerate)
			}
			b.StopTimer()
			if exp := common.HexToHash("fecc4e1fce05c888c8acc8baa2d7677a531714668b7a09b5ede6e3e110be266b"); got != exp {
				b.Fatalf("Error: got %x exp %x", got, exp)
			}

		})
		b.Run("stack", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			var got common.Hash
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				got = generateTrieRoot(it, StackGenerate)
			}
			b.StopTimer()
			if exp := common.HexToHash("fecc4e1fce05c888c8acc8baa2d7677a531714668b7a09b5ede6e3e110be266b"); got != exp {
				b.Fatalf("Error: got %x exp %x", got, exp)
			}

		})
	})
	b.Run("10K", func(b *testing.B) {
		// 4K accounts
		snaps.Update(common.HexToHash("0x02"), common.HexToHash("0x01"), nil, makeAccounts(10000), nil)
		head := snaps.Snapshot(common.HexToHash("0x02"))
		// Call it once to make it create the lists before test starts
		head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
		b.Run("standard", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				generateTrieRoot(it, StdGenerate)
			}
		})
		b.Run("pruning", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				generateTrieRoot(it, PruneGenerate)
			}
		})
		b.Run("stack", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				it := head.(*diffLayer).AccountIterator(common.HexToHash("0x00"))
				generateTrieRoot(it, StackGenerate)
			}
		})
	})
}

func TestStackVsStandard(t *testing.T) {
	type kv struct {
		key   string
		value string
	}
	vals := []kv{
		{key: "04f0860f1d82f4f0e61a03038cb0ffc08d15e22cb3d91d902c8acc32fa709b95", value: "f8440180a08e762c2b29fb1357d0794271a4dbe16167d8b28f1792ad9f78cad08206816127a010b37de11f39e0a372615c70e1d4d7c613937e8f61823d59be9bea62112e175c"},
		{key: "04f0862f9177d381deeed0e6af3b0751f3cce6887746ba13cf41aa1c4dbf6591", value: "f8440180a014baf10561054a68fe522434b4d4c25e1b377e745bf1d676afa71bc891cacf9ba0debc58a981ca4f637e282ab5985d169a0237d03ea9336bc3434d9dce79e62ab3"},
		{key: "04f0a6c0cb97e624bcb799f7d88717fe7fe4894877a8987a27d4792c36a2833e", value: "f8440180a0880595df1b6b3923e8036106cb641aae6b1249faa02d3217da8c556c0fff172ba06569f607421e3779a571977d84910e1177059946e0a064e487b1502e6a282623"},
	}
	stackT := trie.NewStackTrie()
	stdT, _ := trie.New(common.Hash{}, trie.NewDatabase(memorydb.New()))
	for _, kv := range vals {
		stackT.TryUpdate(common.FromHex(kv.key), common.FromHex(kv.value))
		stdT.TryUpdate(common.FromHex(kv.key), common.FromHex(kv.value))
	}
	if got, exp := stackT.Hash(), stdT.Hash(); got != exp {
		t.Errorf("Hash mismatch, got %x, exp %x", got, exp)
	}
}

func TestReStackTrieLeafInsert(t *testing.T) {
	root := trie.NewReStackTrie()
	root.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 17}, []byte{248, 84, 136, 201, 58, 199, 94, 92, 47, 25, 82, 136, 85, 30, 0, 108, 3, 217, 199, 45, 160, 38, 197, 164, 24, 42, 129, 122, 66, 245, 69, 203, 198, 177, 205, 148, 164, 9, 87, 135, 151, 110, 131, 242, 141, 63, 75, 13, 236, 208, 24, 251, 99, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	root.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 34}, []byte{248, 84, 136, 151, 135, 82, 39, 143, 175, 28, 19, 136, 27, 243, 181, 127, 169, 210, 240, 84, 160, 52, 216, 185, 7, 102, 228, 7, 49, 45, 109, 52, 74, 37, 153, 183, 176, 196, 229, 64, 42, 194, 181, 0, 219, 64, 95, 83, 159, 218, 232, 244, 135, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	root.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 51}, []byte{248, 84, 136, 42, 104, 141, 120, 24, 60, 93, 100, 136, 113, 58, 195, 183, 81, 96, 180, 5, 160, 35, 125, 39, 98, 242, 32, 146, 145, 57, 131, 244, 142, 175, 147, 131, 149, 247, 74, 118, 76, 192, 96, 220, 249, 119, 73, 229, 183, 205, 104, 162, 122, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	ref, _ := trie.New(common.Hash{}, trie.NewDatabase(memorydb.New()))
	ref.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 17}, []byte{248, 84, 136, 201, 58, 199, 94, 92, 47, 25, 82, 136, 85, 30, 0, 108, 3, 217, 199, 45, 160, 38, 197, 164, 24, 42, 129, 122, 66, 245, 69, 203, 198, 177, 205, 148, 164, 9, 87, 135, 151, 110, 131, 242, 141, 63, 75, 13, 236, 208, 24, 251, 99, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	ref.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 34}, []byte{248, 84, 136, 151, 135, 82, 39, 143, 175, 28, 19, 136, 27, 243, 181, 127, 169, 210, 240, 84, 160, 52, 216, 185, 7, 102, 228, 7, 49, 45, 109, 52, 74, 37, 153, 183, 176, 196, 229, 64, 42, 194, 181, 0, 219, 64, 95, 83, 159, 218, 232, 244, 135, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	ref.TryUpdate([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 51}, []byte{248, 84, 136, 42, 104, 141, 120, 24, 60, 93, 100, 136, 113, 58, 195, 183, 81, 96, 180, 5, 160, 35, 125, 39, 98, 242, 32, 146, 145, 57, 131, 244, 142, 175, 147, 131, 149, 247, 74, 118, 76, 192, 96, 220, 249, 119, 73, 229, 183, 205, 104, 162, 122, 160, 197, 210, 70, 1, 134, 247, 35, 60, 146, 126, 125, 178, 220, 199, 3, 192, 229, 0, 182, 83, 202, 130, 39, 59, 123, 250, 216, 4, 93, 133, 164, 112})
	if !bytes.Equal(ref.Hash().Bytes(), root.Hash().Bytes()) {
		t.Fatalf("Invalid hash, expected %s got %s", common.ToHex(ref.Hash().Bytes()), common.ToHex(root.Hash().Bytes()))
	}
}
