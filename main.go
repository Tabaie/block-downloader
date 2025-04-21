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
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	flagStartDate = flag.String("start-date", "-30d", "Start date for blocks (start with - for relative to now)")
	flagEndDate   = flag.String("end-date", "now", "End date for blocks (start with - for relative to now)")
	flagUrl       = flag.String("url", "http://localhost:8545", "RPC URL")
	flagSize      = flag.Uint("max", 0, "Maximum size of randomly chosen blocks in MB. If 0, all blocks are written in succession.")
	flagOut       = flag.String("out", "blocks/", "Output file prefix for blocks. It will be written as blobs, with names consisting of a number appended to the argument.")
	flagBlobSize  = flag.Uint("blobsize", 131072, "Size of each blob in bytes")
	client        *ethclient.Client
)

// parseDate parses a date either in the format YYYY-MM-DD, or as a relative date, now, or in the past(e.g., -30d, -2m).
func parseDate(date string) uint64 {
	now := uint64(time.Now().Unix())
	if strings.ToLower(date) == "now" {
		return now
	}
	if date[0] != '-' {
		parsedTime, err := time.Parse("2006-01-02", date)
		assertNoError(err)
		return uint64(parsedTime.Unix())
	}

	// too simple to use regex
	var unit uint64
	switch strings.ToLower(date[len(date)-1:]) {
	case "h":
		unit = 60 * 60
	case "d":
		unit = 24 * 60 * 60
	case "m":
		unit = 30 * 24 * 60 * 60
	case "y":
		unit = 365 * 24 * 60 * 60
	default:
		panic(fmt.Sprintf("invalid date format: %s", date))
	}
	v, err := strconv.Atoi(date[1 : len(date)-1])
	assertNoError(err)

	return now - uint64(v)*unit
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

	var reporter progressReporter

	out := newWriterWithCounter(*flagOut, *flagBlobSize)

	writeBlock := func(blockNum *big.Int) {
		block, err := client.BlockByNumber(context.Background(), blockNum)
		assertNoError(err)
		assertNoError(v1.EncodeBlockForCompression(block, out))
	}

	maxSize := *flagSize * 1024 * 1024

	if maxSize > 0 {
		reporter.n = maxSize

		span := big.NewInt(endNum - startNum)
		startNum := big.NewInt(startNum)

		for out.Written() < maxSize {
			blockNum, err := rand.Int(rand.Reader, span)
			assertNoError(err)
			writeBlock(blockNum.Add(blockNum, startNum))

			reporter.update(out.Written(), "bytes")

		}
	} else {
		reporter.n = uint(endNum - startNum)

		for i := startNum; i < endNum; i++ {
			writeBlock(big.NewInt(i))
			reporter.update(uint(i-startNum), "blocks")
		}
	}
}

type writer struct {
	file *os.File
	n    uint

	blobW0     uint // starting point for current blob
	blobI      uint
	namePrefix string
	blobSize   uint
}

func newWriterWithCounter(namePrefix string, blobSize uint) *writer {
	file, err := os.Create(namePrefix + "0.blob")
	assertNoError(err)

	return &writer{file: file, namePrefix: namePrefix, blobSize: blobSize}
}

func (w *writer) Write(p []byte) (n int, err error) {
	n, err = w.file.Write(p)
	if err != nil {
		return
	}
	w.n += uint(n)

	if w.n-w.blobW0 >= w.blobSize {
		w.blobW0 = w.n
		w.blobI++
		assertNoError(w.file.Close())
		w.file, err = os.Create(fmt.Sprintf("%s%d.blob", w.namePrefix, w.blobI))
		assertNoError(err)
	}

	return
}

func (w *writer) Written() uint {
	return w.n
}

func (w *writer) Close() {
	assertNoError(w.file.Close())
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
		fmt.Printf("%s %d%%%s (%d/%d)\n", time.Now().Format("2006-01-02 15:04:05"), newPct, of, i, r.n)
	}
	r.pct = newPct
	r.lastReportTime = now
}
