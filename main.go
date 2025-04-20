package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	v1 "github.com/consensys/linea-monorepo/prover/lib/compressor/blob/v1"
	"github.com/ethereum/go-ethereum/ethclient"
	"golang.org/x/exp/constraints"
	"io"
	"math/big"
	"os"
	"time"
)

var (
	flagStartDate = flag.String("start-date", "-00-01-00", "Start date for blocks (start with - for relative to now)")
	flagEndDate   = flag.String("end-date", "-00-00-00", "End date for blocks (start with - for relative to now)")
	flagUrl       = flag.String("url", "http://localhost:8545", "RPC URL")
	flagSize      = flag.Uint("max", 0, "Maximum byte size of randomly chosen blocks. If 0, all blocks are written in succession.")
	flagOut       = flag.String("out", "", "Output file for blocks")
	client        *ethclient.Client
)

func parseDate(date string) uint64 {
	relative := false
	if date[0] == '-' {
		relative = true
		date = date[1:]
	}
	parsedTime, err := time.Parse("2006-01-02", date)
	assertNoError(err)
	if relative {
		parsedTime = time.Now().Add(-parsedTime.Sub(time.Time{}))
	}
	return uint64(parsedTime.Unix())
}

// binarySearchF returns the ceiling of a root to increasingF in the range [lower, upper),
// assuming it takes a non-positive value on lower and a positive one on upper.
func binarySearchF[T constraints.Integer](lower, upper T, increasingF func(T) int) T {
	for lower < upper {
		mid := (lower + upper) / 2
		v := increasingF(mid)
		if v < 0 {
			lower = mid + 1
		} else if v > 0 {
			upper = mid
		} else {
			return mid
		}
	}
	return lower
}

func findBlockByDate(date uint64) int64 {
	currentBlock, err := client.HeaderByNumber(context.Background(), nil)
	assertNoError(err)
	return binarySearchF(0, currentBlock.Number.Int64(), func(blockNumber int64) int {
		header, err := client.HeaderByNumber(context.Background(), big.NewInt(blockNumber))
		assertNoError(err)
		return cmp.Compare(header.Time, date)
	})
}

func main() {
	flag.Parse()

	var err error
	client, err = ethclient.Dial(*flagUrl)
	assertNoError(err)

	startNum := findBlockByDate(parseDate(*flagStartDate))
	endNum := findBlockByDate(parseDate(*flagEndDate))

	var reporter *progressReporter
	outFile := io.Writer(os.Stdout)
	if *flagOut != "" {
		file, err := os.Create(*flagOut)
		assertNoError(err)
		defer assertNoError(file.Close())
		outFile = file
		reporter = &progressReporter{}
	}
	out := newWriterWithCounter(outFile)

	writeBlock := func(blockNum *big.Int) {
		block, err := client.BlockByNumber(context.Background(), blockNum)
		assertNoError(err)
		assertNoError(v1.EncodeBlockForCompression(block, out))
	}

	if *flagSize > 0 {
		if reporter != nil {
			reporter.n = *flagSize
		}
		span := big.NewInt(endNum - startNum)
		startNum := big.NewInt(startNum)

		for out.Written() < *flagSize {
			blockNum, err := rand.Int(rand.Reader, span)
			assertNoError(err)
			writeBlock(blockNum.Add(blockNum, startNum))
			if reporter != nil {
				reporter.update(out.Written(), "bytes")
			}
		}
	} else {
		if reporter != nil {
			reporter.n = uint(endNum - startNum)
		}
		for i := startNum; i < endNum; i++ {
			writeBlock(big.NewInt(i))
			if reporter != nil {
				reporter.update(uint(i-startNum), "blocks")
			}
		}
	}
}

type writerWithCounter struct {
	w io.Writer
	n uint
}

func newWriterWithCounter(w io.Writer) *writerWithCounter {
	return &writerWithCounter{w: w}
}

func (w *writerWithCounter) Write(p []byte) (n int, err error) {
	n, err = w.w.Write(p)
	if err != nil {
		return
	}
	w.n += uint(n)
	return
}

func (w *writerWithCounter) Written() uint {
	return w.n
}

func assertNoError(err error) {
	if err != nil {
		panic(err)
	}
}

type progressReporter struct {
	n              uint  // max value
	pct            uint  // current percentage
	lastReportTime int64 // last time reported
}

func newProgressReporter(n uint) *progressReporter {
	return &progressReporter{n: n, lastReportTime: time.Now().Unix()}
}

func (r *progressReporter) update(i uint, objectName string) {
	newPct := i * 100 / r.n
	now := time.Now().Unix()
	if newPct != r.pct || now-r.lastReportTime > 30 {
		of := ""
		if objectName != "" {
			of = fmt.Sprintf(" of %s", objectName)
		}
		fmt.Printf("%d%%%s (%d/%d)\n", newPct, of, i, r.n)
	}
	r.pct = newPct
	r.lastReportTime = now
}
