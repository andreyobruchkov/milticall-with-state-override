package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
)

//go:embed multicall.abi
var multicallABI []byte

// Empty on mainnet. A provider that drops the state override executes this account and returns 0x.
var batcher = common.HexToAddress("0x00000000000000000000000000000000000000cA")

type call3 struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

type callResult struct {
	Success    bool
	ReturnData []byte
}

type codeOverride struct {
	Code hexutil.Bytes `json:"code"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		if err := cmdCheck(os.Args[2:]); err != nil {
			fail(err)
		}
	case "balances":
		if err := cmdBalances(os.Args[2:]); err != nil {
			fail(err)
		}
	case "tokens":
		if err := cmdTokens(os.Args[2:]); err != nil {
			fail(err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `multicall — one eth_call, three native balances, via a Multicall3 state override

Usage:
  multicall balances [-rpc URL] <addr> <addr> <addr>
  multicall tokens [-rpc URL]
  multicall check [-solc path]

`)
}

func cmdBalances(args []string) error {
	fs := flag.NewFlagSet("balances", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rpcURL := fs.String("rpc", "https://ethereum.publicnode.com", "Ethereum HTTP RPC")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: multicall balances [-rpc URL] <addr> <addr> <addr>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return fmt.Errorf("want 3 addresses, got %d", fs.NArg())
	}

	addrs := make([]common.Address, 3)
	for i, arg := range fs.Args() {
		if !common.IsHexAddress(arg) {
			return fmt.Errorf("bad address %q", arg)
		}
		addrs[i] = common.HexToAddress(arg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		fail(err)
	}
	defer client.Close()

	block, balances, err := ethBalances(ctx, client, addrs)
	if err != nil {
		fail(err)
	}

	fmt.Printf("block %s\n", block)
	for i, addr := range addrs {
		fmt.Printf("%s  %s ETH\n", addr.Hex(), formatETH(balances[i]))
	}
	return nil
}

func ethBalances(ctx context.Context, client *ethclient.Client, addrs []common.Address) (*big.Int, []*big.Int, error) {
	block, err := client.BlockNumber(ctx)
	if err != nil {
		return nil, nil, err
	}
	blockNum := new(big.Int).SetUint64(block)

	code, err := client.CodeAt(ctx, batcher, blockNum)
	if err != nil {
		return nil, nil, err
	}
	if len(code) != 0 {
		return nil, nil, fmt.Errorf("batcher %s has code at block %d", batcher.Hex(), block)
	}

	parsed, err := abi.JSON(bytes.NewReader(multicallABI))
	if err != nil {
		return nil, nil, err
	}

	calls := make([]call3, len(addrs))
	for i, addr := range addrs {
		data, err := parsed.Pack("getEthBalance", addr)
		if err != nil {
			return nil, nil, err
		}
		calls[i] = call3{Target: batcher, AllowFailure: true, CallData: data}
	}
	results, err := aggregate3(ctx, client, blockNum, parsed, calls)
	if err != nil {
		return nil, nil, err
	}

	balances := make([]*big.Int, len(addrs))
	for i, res := range results {
		if !res.Success {
			return nil, nil, fmt.Errorf("getEthBalance(%s) failed", addrs[i].Hex())
		}
		vals, err := parsed.Unpack("getEthBalance", res.ReturnData)
		if err != nil {
			return nil, nil, fmt.Errorf("decode %s: %w", addrs[i].Hex(), err)
		}
		balances[i] = vals[0].(*big.Int)
	}
	return blockNum, balances, nil
}

func aggregate3(ctx context.Context, client *ethclient.Client, blockNum *big.Int, parsed abi.ABI, calls []call3) ([]callResult, error) {
	input, err := parsed.Pack("aggregate3", calls)
	if err != nil {
		return nil, err
	}

	var out hexutil.Bytes
	err = client.Client().CallContext(ctx, &out, "eth_call",
		map[string]any{
			"to":   batcher,
			"data": hexutil.Bytes(input),
		},
		hexutil.EncodeBig(blockNum),
		map[common.Address]codeOverride{
			batcher: {Code: multicallRuntime},
		},
	)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty eth_call result: state override was not applied")
	}

	var results []callResult
	if err := parsed.UnpackIntoInterface(&results, "aggregate3", out); err != nil {
		return nil, err
	}
	if len(results) != len(calls) {
		return nil, fmt.Errorf("got %d results, want %d", len(results), len(calls))
	}
	return results, nil
}

func formatETH(wei *big.Int) string {
	eth := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e18))
	return eth.Text('f', 6)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
