package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	solcFlag := fs.String("solc", "", "path to solc 0.8.12 (default: SOLC, then PATH, then /tmp/solc-bin/solc)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: multicall check [-solc path]")
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
		return fmt.Errorf("check takes no addresses")
	}

	solc, err := findSolc(*solcFlag)
	if err != nil {
		return err
	}
	version, err := exec.Command(solc, "--version").Output()
	if err != nil {
		return fmt.Errorf("run %s: %w", solc, err)
	}
	if !strings.Contains(string(version), "0.8.12") {
		return fmt.Errorf("%s is %s, want 0.8.12", solc, firstLine(version))
	}

	fmt.Printf("compiling Multicall3.sol\n  %s\n", solcVersionLine(version))
	cmd := exec.Command(solc,
		"--optimize",
		"--optimize-runs", "10000000",
		"--metadata-hash", "ipfs",
		"--bin-runtime",
		"Multicall3.sol",
	)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("solc: %s", bytes.TrimSpace(exit.Stderr))
		}
		return err
	}

	compiled, err := lastHexLine(out)
	if err != nil {
		return err
	}
	if !bytes.Equal(compiled, multicallRuntime) {
		return fmt.Errorf("compiled runtime is %d bytes, pinned runtime is %d bytes", len(compiled), len(multicallRuntime))
	}
	fmt.Printf("ok  runtime matches (%d bytes)\n", len(compiled))
	return nil
}

func findSolc(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if env := os.Getenv("SOLC"); env != "" {
		return env, nil
	}
	if path, err := exec.LookPath("solc"); err == nil {
		return path, nil
	}
	const fallback = "/tmp/solc-bin/solc"
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", fmt.Errorf("solc 0.8.12 not found (pass -solc, or set SOLC)")
}

func lastHexLine(out []byte) ([]byte, error) {
	var hexLine string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 2 && isHex(line) {
			hexLine = line
		}
	}
	if hexLine == "" {
		return nil, fmt.Errorf("solc produced no runtime bytecode")
	}
	return decodeHex(hexLine)
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length bytecode")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, ok := fromHex(s[i*2])
		lo, ok2 := fromHex(s[i*2+1])
		if !ok || !ok2 {
			return nil, fmt.Errorf("bad bytecode hex")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func solcVersionLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return line
		}
	}
	return firstLine(b)
}

func firstLine(b []byte) string {
	line, _, _ := bytes.Cut(b, []byte("\n"))
	return strings.TrimSpace(string(line))
}
