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

package vm

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/bn256"
	"github.com/ethereum/go-ethereum/params"
	"github.com/go-interpreter/wagon/exec"
	"github.com/go-interpreter/wagon/wasm"
	"golang.org/x/crypto/ripemd160"
)

// PrecompiledContract is the basic interface for native Go contracts. The implementation
// requires a deterministic gas count based on the input size of the Run method of the
// contract.
type PrecompiledContract interface {
	RequiredGas(input []byte) uint64                      // RequiredPrice calculates the contract gas use
	Run(input []byte, contract *Contract) ([]byte, error) // Run runs the precompiled contract
	Addr() int
}

// PrecompiledContractsHomestead contains the default set of pre-compiled Ethereum
// contracts used in the Frontier and Homestead releases.
var PrecompiledContractsHomestead = map[common.Address]PrecompiledContract{
	common.BytesToAddress([]byte{1}): &ecrecover{},
	common.BytesToAddress([]byte{2}): &sha256hash{},
	common.BytesToAddress([]byte{3}): &ripemd160hash{},
	common.BytesToAddress([]byte{4}): &dataCopy{},
}

// PrecompiledContractsByzantium contains the default set of pre-compiled Ethereum
// contracts used in the Byzantium release.
var PrecompiledContractsByzantium = map[common.Address]PrecompiledContract{
	common.BytesToAddress([]byte{1}): &ecrecover{},
	common.BytesToAddress([]byte{2}): &sha256hash{},
	common.BytesToAddress([]byte{3}): &ripemd160hash{},
	common.BytesToAddress([]byte{4}): &dataCopy{},
	common.BytesToAddress([]byte{5}): &bigModExp{},
	common.BytesToAddress([]byte{6}): &bn256Add{},
	common.BytesToAddress([]byte{7}): &bn256ScalarMul{},
	common.BytesToAddress([]byte{8}): &bn256Pairing{},
}

// PrecompiledContractsEWASM contains the default set of pre-compiled Ethereum
// contracts used for Ethereum 1.x release.
var PrecompiledContractsEWASM = map[common.Address]PrecompiledContract{
	common.BytesToAddress([]byte{1}): newEWASMPrecompile(ewasmEcrecoverCode, 1),
	common.BytesToAddress([]byte{2}): newEWASMPrecompile(ewasmSha256HashCode, 2),
	common.BytesToAddress([]byte{3}): newEWASMPrecompile(ewasmRipemd160hashCode, 3),
	common.BytesToAddress([]byte{4}): newEWASMPrecompile(ewasmIdentityCode, 4),
	common.BytesToAddress([]byte{5}): newEWASMPrecompile(ewasmExpmodCode, 5),
	common.BytesToAddress([]byte{6}): newEWASMPrecompile(ewasmEcaddCode, 6),
	common.BytesToAddress([]byte{7}): newEWASMPrecompile(ewasmEcmulCode, 7),
	common.BytesToAddress([]byte{8}): newEWASMPrecompile(ewasmEcpairingCode, 8),
}

var percontractW [9]int64
var percontractN [9]int64
var count [9]int64
var totalcount = 0
var nerrors [9]int64

// RunPrecompiledContract runs and evaluates the output of a precompiled contract.
func RunPrecompiledContract(p PrecompiledContract, input []byte, contract *Contract) (ret []byte, err error) {
	totalcount++
	defer func() {
		if totalcount%100 == 0 {
			fmt.Print("~~~~~ Averages: ")
			for i, t := range percontractW {
				avgW, avgN := float64(0), float64(0)
				if count[i] != 0 {
					avgW = float64(t) / float64(count[i])
					avgN = float64(percontractN[i]) / float64(count[i])
				}
				fmt.Print(" ", i, "=", avgW, avgN)
			}
			fmt.Println("nerr=", nerrors, "total", totalcount)
		}
	}()

	gas := p.RequiredGas(input)
	if gas <= contract.Gas {
		start := time.Now().UnixNano()
		ref, rerr := p.Run(input, contract)
		native := time.Now().UnixNano()
		// fmt.Println("standard result:", ref, rerr)
		res, err := PrecompiledContractsEWASM[common.BigToAddress(big.NewInt(int64(p.Addr())))].Run(input, contract)
		end := time.Now().UnixNano()
		percontractW[p.Addr()] += end - native
		percontractN[p.Addr()] += native - start
		count[p.Addr()]++
		// fmt.Println("precompile result:", res, err)
		if (err == nil) != (rerr == nil) || bytes.Compare(res, ref) != 0 {
			fmt.Println("&&&&&&&&&&&&&&&&&&&&& Difference for conract ", p.Addr(), res, ref, err, rerr, input, p.Addr(), contract.Address())
			panic("coucou")
		} else {
			return res, err
		}
	}
	return nil, ErrOutOfGas
}

type ewasmPrecompile struct {
	code     []byte
	vm       *exec.VM
	contract *Contract
	retData  []byte
	addr     int
	idx      uint32
	input    []byte
}

// This is a subset of the functions available in the full EEI at
// https://github.com/ethereum/go-ethereum/pull/16957. It is safer
// not to provide all functions in case of a vulnerability in the
// interpreter.
var eeiFunctionList = []string{
	"useGas",
	"callDataCopy",
	"getCallDataSize",
	"finish",
	"revert",
}

func moduleResolver(name string, precompile *ewasmPrecompile) (*wasm.Module, error) {
	if name != "ethereum" {
		return nil, fmt.Errorf("Unknown module name: %s", name)
	}

	m := wasm.NewModule()
	m.Types = &wasm.SectionTypes{
		Entries: []wasm.FunctionSig{
			{
				ParamTypes:  []wasm.ValueType{wasm.ValueTypeI64},
				ReturnTypes: []wasm.ValueType{},
			},
			{
				ParamTypes:  []wasm.ValueType{wasm.ValueTypeI32, wasm.ValueTypeI32},
				ReturnTypes: []wasm.ValueType{},
			},
			{
				ParamTypes:  []wasm.ValueType{wasm.ValueTypeI32, wasm.ValueTypeI32, wasm.ValueTypeI32},
				ReturnTypes: []wasm.ValueType{},
			},
			{
				ParamTypes:  []wasm.ValueType{},
				ReturnTypes: []wasm.ValueType{wasm.ValueTypeI32},
			},
		},
	}
	m.FunctionIndexSpace = []wasm.Function{
		{
			Sig: &m.Types.Entries[0],
			Host: reflect.ValueOf(func(p *exec.Process, a int64) {
				// fmt.Println("use gas", a)
				precompile.contract.UseGas(uint64(a))
			}),
			Body: &wasm.FunctionBody{},
		},
		{
			Sig: &m.Types.Entries[2],
			Host: reflect.ValueOf(func(p *exec.Process, r, d, l int32) {
				if l > 0 {
					// Unlike regular EEI functions, the gas is not charged at this
					// time but I'm leaving that code here for future reference.
					// in.gasAccounting(GasCostVeryLow + GasCostCopy*(uint64(l+31)>>5))
					p.WriteAt(precompile.input[d:d+l], int64(r))
				}
			}),
			Body: &wasm.FunctionBody{},
		},
		{
			Sig: &m.Types.Entries[3],
			Host: reflect.ValueOf(func(p *exec.Process) int32 {
				// fmt.Println("data size",  int32(len(precompile.input)))
				return int32(len(precompile.input))
			}),
			Body: &wasm.FunctionBody{},
		},
		{
			Sig: &m.Types.Entries[1],
			Host: reflect.ValueOf(func(p *exec.Process, d, l int32) {
				// fmt.Println("finish", d, l)
				precompile.retData = make([]byte, int64(l))
				p.ReadAt(precompile.retData, int64(d))
				p.Terminate()
			}),
			Body: &wasm.FunctionBody{},
		},
		{
			Sig: &m.Types.Entries[1],
			Host: reflect.ValueOf(func(p *exec.Process, d, l int32) {
				fmt.Println("revert")
				precompile.retData = make([]byte, int64(l))
				p.ReadAt(precompile.retData, int64(d))
				p.Terminate()
			}),
			Body: &wasm.FunctionBody{},
		},
	}

	entries := make(map[string]wasm.ExportEntry)

	for idx, name := range eeiFunctionList {
		entries[name] = wasm.ExportEntry{
			FieldStr: name,
			Kind:     wasm.ExternalFunction,
			Index:    uint32(idx),
		}
	}

	m.Export = &wasm.SectionExports{
		Entries: entries,
	}

	return m, nil
}

func newEWASMPrecompile(code []byte, addr int) *ewasmPrecompile {
	ret := &ewasmPrecompile{}
	module, err := wasm.ReadModule(bytes.NewReader(code), func(s string) (*wasm.Module, error) {
		return moduleResolver(s, ret)
	})
	if err != nil {
		panic(fmt.Sprintf("Could not read precompile module: %v", err))
	}

	for name, export := range module.Export.Entries {
		if name == "main" && export.Kind == wasm.ExternalFunction {
			ret.idx = export.Index
		}
	}

	vm, err := exec.NewVM(module)
	if err != nil {
		panic("Could not create precompile VM")
	}
	vm.RecoverPanic = true
	ret.vm = vm
	ret.addr = addr

	return ret
}

func (c *ewasmPrecompile) RequiredGas(input []byte) uint64 {
	return 0
}

func (c *ewasmPrecompile) Addr() int {
	return c.addr
}

func (c *ewasmPrecompile) Run(input []byte, contract *Contract) ([]byte, error) {
	c.vm.Restart()
	mem := c.vm.Memory()

	/* Copy input into memory */
	if len(input) > len(mem) {
		return nil, fmt.Errorf("input size (%d) is greater than available memory (%d)", len(input), len(mem))
	}

	c.input = input
	c.contract = contract
	defer func() {
		c.input = nil
		c.contract = nil
	}()

	/* Run the contract */
	_, err := c.vm.ExecCode(int64(c.idx))
	if err == nil {
		return c.retData, nil
	}
	return nil, err
}

// ECRECOVER implemented as a native contract.
type ecrecover struct{}

func (c *ecrecover) RequiredGas(input []byte) uint64 {
	return params.EcrecoverGas
}

func (c *ecrecover) Addr() int {
	return 1
}

func (c *ecrecover) Run(input []byte, contract *Contract) ([]byte, error) {
	const ecRecoverInputLength = 128

	input = common.RightPadBytes(input, ecRecoverInputLength)
	// "input" is (hash, v, r, s), each 32 bytes
	// but for ecrecover we want (r, s, v)

	r := new(big.Int).SetBytes(input[64:96])
	s := new(big.Int).SetBytes(input[96:128])
	v := input[63] - 27

	// tighter sig s values input homestead only apply to tx sigs
	if !allZero(input[32:63]) || !crypto.ValidateSignatureValues(v, r, s, false) {
		fmt.Println(input[32:63], v)
		return nil, nil
	}

	// v needs to be at the end for libsecp256k1
	pubKey, err := crypto.Ecrecover(input[:32], append(input[64:128], v))
	// make sure the public key is a valid one
	if err != nil {
		return nil, nil
	}

	// the first byte of pubkey is bitcoin heritage
	return common.LeftPadBytes(crypto.Keccak256(pubKey[1:])[12:], 32), nil
}

// SHA256 implemented as a native contract.
type sha256hash struct{}

// RequiredGas returns the gas required to execute the pre-compiled contract.
//
// This method does not require any overflow checking as the input size gas costs
// required for anything significant is so high it's impossible to pay for.
func (c *sha256hash) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*params.Sha256PerWordGas + params.Sha256BaseGas
}
func (c *sha256hash) Run(input []byte, contract *Contract) ([]byte, error) {
	h := sha256.Sum256(input)
	return h[:], nil
}

func (c *sha256hash) Addr() int {
	return 2
}

// RIPEMD160 implemented as a native contract.
type ripemd160hash struct{}

// RequiredGas returns the gas required to execute the pre-compiled contract.
//
// This method does not require any overflow checking as the input size gas costs
// required for anything significant is so high it's impossible to pay for.
func (c *ripemd160hash) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*params.Ripemd160PerWordGas + params.Ripemd160BaseGas
}

func (c *ripemd160hash) Addr() int {
	return 3
}

func (c *ripemd160hash) Run(input []byte, contract *Contract) ([]byte, error) {
	ripemd := ripemd160.New()
	ripemd.Write(input)
	return common.LeftPadBytes(ripemd.Sum(nil), 32), nil
}

// data copy implemented as a native contract.
type dataCopy struct{}

// RequiredGas returns the gas required to execute the pre-compiled contract.
//
// This method does not require any overflow checking as the input size gas costs
// required for anything significant is so high it's impossible to pay for.
func (c *dataCopy) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*params.IdentityPerWordGas + params.IdentityBaseGas
}
func (c *dataCopy) Run(in []byte, contract *Contract) ([]byte, error) {
	return in, nil
}

func (c *dataCopy) Addr() int {
	return 4
}

// bigModExp implements a native big integer exponential modular operation.
type bigModExp struct{}

var (
	big1      = big.NewInt(1)
	big4      = big.NewInt(4)
	big8      = big.NewInt(8)
	big16     = big.NewInt(16)
	big32     = big.NewInt(32)
	big64     = big.NewInt(64)
	big96     = big.NewInt(96)
	big480    = big.NewInt(480)
	big1024   = big.NewInt(1024)
	big3072   = big.NewInt(3072)
	big199680 = big.NewInt(199680)
)

// RequiredGas returns the gas required to execute the pre-compiled contract.
func (c *bigModExp) RequiredGas(input []byte) uint64 {
	var (
		baseLen = new(big.Int).SetBytes(getData(input, 0, 32))
		expLen  = new(big.Int).SetBytes(getData(input, 32, 32))
		modLen  = new(big.Int).SetBytes(getData(input, 64, 32))
	)
	// fmt.Println(input, baseLen, expLen, modLen)
	if len(input) > 96 {
		input = input[96:]
	} else {
		input = input[:0]
	}
	// Retrieve the head 32 bytes of exp for the adjusted exponent length
	var expHead *big.Int
	if big.NewInt(int64(len(input))).Cmp(baseLen) <= 0 {
		expHead = new(big.Int)
	} else {
		if expLen.Cmp(big32) > 0 {
			expHead = new(big.Int).SetBytes(getData(input, baseLen.Uint64(), 32))
		} else {
			expHead = new(big.Int).SetBytes(getData(input, baseLen.Uint64(), expLen.Uint64()))
		}
	}
	// Calculate the adjusted exponent length
	var msb int
	if bitlen := expHead.BitLen(); bitlen > 0 {
		msb = bitlen - 1
	}
	adjExpLen := new(big.Int)
	if expLen.Cmp(big32) > 0 {
		adjExpLen.Sub(expLen, big32)
		adjExpLen.Mul(big8, adjExpLen)
	}
	adjExpLen.Add(adjExpLen, big.NewInt(int64(msb)))

	// Calculate the gas cost of the operation
	// fmt.Println(math.BigMax(modLen, baseLen))
	gas := new(big.Int).Set(math.BigMax(modLen, baseLen))
	switch {
	case gas.Cmp(big64) <= 0:
		gas.Mul(gas, gas)
	case gas.Cmp(big1024) <= 0:
		gas = new(big.Int).Add(
			new(big.Int).Div(new(big.Int).Mul(gas, gas), big4),
			new(big.Int).Sub(new(big.Int).Mul(big96, gas), big3072),
		)
	default:
		gas = new(big.Int).Add(
			new(big.Int).Div(new(big.Int).Mul(gas, gas), big16),
			new(big.Int).Sub(new(big.Int).Mul(big480, gas), big199680),
		)
	}
	gas.Mul(gas, math.BigMax(adjExpLen, big1))
	gas.Div(gas, new(big.Int).SetUint64(params.ModExpQuadCoeffDiv))

	if gas.BitLen() > 64 {
		return math.MaxUint64
	}
	// fmt.Println("gas==", gas, gas.Uint64())
	return gas.Uint64()
}

func (c *bigModExp) Run(input []byte, contract *Contract) ([]byte, error) {
	var (
		baseLen = new(big.Int).SetBytes(getData(input, 0, 32)).Uint64()
		expLen  = new(big.Int).SetBytes(getData(input, 32, 32)).Uint64()
		modLen  = new(big.Int).SetBytes(getData(input, 64, 32)).Uint64()
	)
	if len(input) > 96 {
		input = input[96:]
	} else {
		input = input[:0]
	}
	// Handle a special case when both the base and mod length is zero
	if baseLen == 0 && modLen == 0 {
		return []byte{}, nil
	}
	// Retrieve the operands and execute the exponentiation
	var (
		base = new(big.Int).SetBytes(getData(input, 0, baseLen))
		exp  = new(big.Int).SetBytes(getData(input, baseLen, expLen))
		mod  = new(big.Int).SetBytes(getData(input, baseLen+expLen, modLen))
	)
	// fmt.Println("expmod", base, exp, mod)
	if mod.BitLen() == 0 {
		// Modulo 0 is undefined, return zero
		return common.LeftPadBytes([]byte{}, int(modLen)), nil
	}
	return common.LeftPadBytes(base.Exp(base, exp, mod).Bytes(), int(modLen)), nil
}

func (c *bigModExp) Addr() int {
	return 5
}

// newCurvePoint unmarshals a binary blob into a bn256 elliptic curve point,
// returning it, or an error if the point is invalid.
func newCurvePoint(blob []byte) (*bn256.G1, error) {
	p := new(bn256.G1)
	if _, err := p.Unmarshal(blob); err != nil {
		return nil, err
	}
	return p, nil
}

// newTwistPoint unmarshals a binary blob into a bn256 elliptic curve point,
// returning it, or an error if the point is invalid.
func newTwistPoint(blob []byte) (*bn256.G2, error) {
	p := new(bn256.G2)
	if _, err := p.Unmarshal(blob); err != nil {
		return nil, err
	}
	return p, nil
}

// bn256Add implements a native elliptic curve point addition.
type bn256Add struct{}

func (c *bn256Add) Addr() int {
	return 6
}

// RequiredGas returns the gas required to execute the pre-compiled contract.
func (c *bn256Add) RequiredGas(input []byte) uint64 {
	return params.Bn256AddGas
}

func (c *bn256Add) Run(input []byte, contract *Contract) ([]byte, error) {
	x, err := newCurvePoint(getData(input, 0, 64))
	if err != nil {
		return nil, err
	}
	y, err := newCurvePoint(getData(input, 64, 64))
	if err != nil {
		return nil, err
	}
	res := new(bn256.G1)
	res.Add(x, y)
	return res.Marshal(), nil
}

// bn256ScalarMul implements a native elliptic curve scalar multiplication.
type bn256ScalarMul struct{}

func (c *bn256ScalarMul) Addr() int {
	return 7
}

// RequiredGas returns the gas required to execute the pre-compiled contract.
func (c *bn256ScalarMul) RequiredGas(input []byte) uint64 {
	return params.Bn256ScalarMulGas
}

func (c *bn256ScalarMul) Run(input []byte, contract *Contract) ([]byte, error) {
	p, err := newCurvePoint(getData(input, 0, 64))
	if err != nil {
		return nil, err
	}
	res := new(bn256.G1)
	res.ScalarMult(p, new(big.Int).SetBytes(getData(input, 64, 32)))
	return res.Marshal(), nil
}

var (
	// true32Byte is returned if the bn256 pairing check succeeds.
	true32Byte = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

	// false32Byte is returned if the bn256 pairing check fails.
	false32Byte = make([]byte, 32)

	// errBadPairingInput is returned if the bn256 pairing input is invalid.
	errBadPairingInput = errors.New("bad elliptic curve pairing size")
)

// bn256Pairing implements a pairing pre-compile for the bn256 curve
type bn256Pairing struct{}

func (c *bn256Pairing) Addr() int {
	return 8
}

// RequiredGas returns the gas required to execute the pre-compiled contract.
func (c *bn256Pairing) RequiredGas(input []byte) uint64 {
	return params.Bn256PairingBaseGas + uint64(len(input)/192)*params.Bn256PairingPerPointGas
}

func (c *bn256Pairing) Run(input []byte, contract *Contract) ([]byte, error) {
	// Handle some corner cases cheaply
	if len(input)%192 > 0 {
		return nil, errBadPairingInput
	}
	// Convert the input into a set of coordinates
	var (
		cs []*bn256.G1
		ts []*bn256.G2
	)
	for i := 0; i < len(input); i += 192 {
		c, err := newCurvePoint(input[i : i+64])
		if err != nil {
			return nil, err
		}
		t, err := newTwistPoint(input[i+64 : i+192])
		if err != nil {
			return nil, err
		}
		cs = append(cs, c)
		ts = append(ts, t)
	}
	// Execute the pairing checks and return the results
	if bn256.PairingCheck(cs, ts) {
		return true32Byte, nil
	}
	return false32Byte, nil
}
