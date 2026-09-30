package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const erc20ABI = `[
  {"name":"balanceOf","type":"function","stateMutability":"view","inputs":[{"name":"account","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
  {"name":"decimals","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]}
]`

type tokenPair struct {
	wallet common.Address
	token  common.Address
}

const (
	wallet1 = "0xFEEEEEE44046c3f61a8CC081E0918eF0de0a7ffC"
	token1  = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	wallet2 = "0x3F6452fcCFa1e70AA5f6b23EE7a03918Ee203552"
	token2  = "0x60597bFF98bFb335F57d08Bd2d23f96eE0015bDC"
	wallet3 = "0xdb9B1e94B5b69Df7e401DDbedE43491141047dB3"
	token3  = "0x2b591e99afE9f32eAA6214f7B7629768c40Eeb39"
)

var tokenPairs = []tokenPair{
	{common.HexToAddress(wallet1), common.HexToAddress(token1)},
	{common.HexToAddress(wallet2), common.HexToAddress(token2)},
	{common.HexToAddress(wallet3), common.HexToAddress(token3)},
}

func cmdTokens(args []string) error {
	fs := flag.NewFlagSet("tokens", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rpcURL := fs.String("rpc", "https://ethereum.publicnode.com", "Ethereum HTTP RPC")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: multicall tokens [-rpc URL]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return fmt.Errorf("tokens takes no addresses; the three pairs are fixed in the source")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		return err
	}
	defer client.Close()

	block, lines, err := tokenBalances(ctx, client, tokenPairs)
	if err != nil {
		return err
	}
	fmt.Printf("block %s\n", block)
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func tokenBalances(ctx context.Context, client *ethclient.Client, pairs []tokenPair) (*big.Int, []string, error) {
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

	multi, err := abi.JSON(strings.NewReader(string(multicallABI)))
	if err != nil {
		return nil, nil, err
	}
	erc20, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, nil, err
	}

	calls := make([]call3, 0, len(pairs)*2)
	for _, pair := range pairs {
		bal, err := erc20.Pack("balanceOf", pair.wallet)
		if err != nil {
			return nil, nil, err
		}
		dec, err := erc20.Pack("decimals")
		if err != nil {
			return nil, nil, err
		}
		calls = append(calls,
			call3{Target: pair.token, AllowFailure: true, CallData: bal},
			call3{Target: pair.token, AllowFailure: true, CallData: dec},
		)
	}

	results, err := aggregate3(ctx, client, blockNum, multi, calls)
	if err != nil {
		return nil, nil, err
	}

	lines := make([]string, len(pairs))
	for i, pair := range pairs {
		balRes := results[i*2]
		decRes := results[i*2+1]
		if !balRes.Success {
			return nil, nil, fmt.Errorf("balanceOf(%s) on %s failed", pair.wallet.Hex(), pair.token.Hex())
		}
		vals, err := erc20.Unpack("balanceOf", balRes.ReturnData)
		if err != nil {
			return nil, nil, fmt.Errorf("decode %s: %w", pair.token.Hex(), err)
		}
		amount := vals[0].(*big.Int)
		shown := amount.String()
		if decRes.Success {
			dvals, err := erc20.Unpack("decimals", decRes.ReturnData)
			if err == nil {
				shown = formatUnits(amount, int(dvals[0].(uint8)))
			}
		}
		lines[i] = fmt.Sprintf("%s  %s  %s", pair.wallet.Hex(), pair.token.Hex(), shown)
	}
	return blockNum, lines, nil
}

func formatUnits(amount *big.Int, decimals int) string {
	if decimals == 0 {
		return amount.String()
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	rat := new(big.Rat).SetFrac(amount, scale)
	return rat.FloatString(min(decimals, 6))
}
