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
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/trie"
)

type leaf struct {
	key   common.Hash
	value []byte
}

// trieGenerator is a very basic hexary trie builder which uses the same Trie
// as the rest of geth, with no enhancements or optimizations
type trieGenerator struct{}

func (gen *trieGenerator) Generate(in chan (leaf), out chan (common.Hash)) {
	t, _ := trie.New(common.Hash{}, trie.NewDatabase(memorydb.New()))
	for leaf := range in {
		t.TryUpdate(leaf.key[:], leaf.value)
	}
	out <- t.Hash()
}
