// Command gen writes synthetic NDJSON access-log records to stdout, for
// measuring ndstats on large inputs.
//
// It lives under testdata/ so that `go build ./...`, `go vet ./...` and
// `go test ./...` skip it; run it explicitly:
//
//	go run ./testdata/gen -lines 25000000 > big.ndjson      # ~3 GB
//	go run ./testdata/gen -size 1G > big.ndjson              # stop at ~1 GB
//	go run ./testdata/gen -lines 1000000 -groups 1000000 ... # high cardinality
//
// Output is written as it is generated, so the generator itself runs in
// constant memory however large the file.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
)

var (
	methods = []string{"GET", "GET", "GET", "POST", "PUT", "DELETE"}
	paths   = []string{"/api/v2/orders", "/api/v2/orders/checkout", "/api/v2/users/me", "/api/v2/products", "/healthz"}
	regions = []string{"us-east", "us-west", "eu-west", "eu-central", "ap-south", "ap-northeast"}
	// Weighted so 200 dominates, as in real traffic.
	statuses = []int{200, 200, 200, 200, 200, 200, 200, 201, 204, 301, 400, 404, 404, 429, 500, 503}
)

func main() {
	lines := flag.Int("lines", 0, "number of lines to write (0 = use -size)")
	size := flag.String("size", "", "stop after about this many bytes, e.g. 500M or 2G")
	groups := flag.Int("groups", 0, "if > 0, use this many distinct regions (region-0, region-1, …) instead of the six real ones")
	bad := flag.Float64("bad", 0.001, "fraction of lines that are bad (truncated JSON, missing region, unusable latency, blank)")
	seed := flag.Uint64("seed", 1, "random seed; the same seed gives the same file")
	flag.Parse()

	maxBytes, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(2)
	}
	if *lines <= 0 && maxBytes <= 0 {
		fmt.Fprintln(os.Stderr, "gen: set -lines or -size")
		flag.Usage()
		os.Exit(2)
	}

	rng := rand.New(rand.NewPCG(*seed, *seed))
	w := bufio.NewWriterSize(os.Stdout, 1<<20)

	var written int64
	for i := 0; *lines <= 0 || i < *lines; i++ {
		if maxBytes > 0 && written >= maxBytes {
			break
		}
		line := record(rng, i, *groups, *bad)
		n, err := w.WriteString(line)
		if err != nil {
			// Usually a closed pipe, e.g. `gen ... | head`.
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
		written += int64(n)
	}

	// Flush explicitly rather than with defer: the last buffered chunk is
	// only written here, so its error matters, and os.Exit skips defers.
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

// record returns one NDJSON line, including its trailing newline.
func record(rng *rand.Rand, i, groups int, bad float64) string {
	region := regions[rng.IntN(len(regions))]
	if groups > 0 {
		region = "region-" + strconv.Itoa(rng.IntN(groups))
	}

	line := fmt.Sprintf(
		`{"ts":"2026-11-14T%02d:%02d:%02d.%03dZ","method":%q,"path":%q,"status":%d,"region":%q,"latency_ms":%s,"bytes":%d,"request_id":"req_%016x"}`,
		rng.IntN(24), rng.IntN(60), rng.IntN(60), rng.IntN(1000),
		methods[rng.IntN(len(methods))], paths[rng.IntN(len(paths))],
		statuses[rng.IntN(len(statuses))], region,
		strconv.FormatFloat(latency(rng), 'f', 1, 64),
		rng.IntN(64<<10), uint64(i)*0x9E3779B97F4A7C15, // unique per line
	)

	if rng.Float64() < bad {
		line = badLine(rng, line)
	}
	return line + "\n"
}

// latency is log-normal: most requests are fast, with a long slow tail,
// which is what makes percentiles interesting.
func latency(rng *rand.Rand) float64 {
	return math.Round(math.Exp(3+0.9*rng.NormFloat64())*10) / 10
}

// badLine damages a good line in one of the ways ndstats has to handle.
func badLine(rng *rand.Rand, line string) string {
	switch rng.IntN(4) {
	case 0: // truncated JSON
		return line[:len(line)/2]
	case 1: // missing group key
		return strings.Replace(line, `"region":`, `"zone":`, 1)
	case 2: // unusable value: still a valid line, counted in the group's skipped column
		start := strings.Index(line, `"latency_ms":`) + len(`"latency_ms":`)
		end := start + strings.Index(line[start:], ",")
		return line[:start] + `"fast"` + line[end:]
	default: // blank line
		return ""
	}
}

// parseSize turns "500M", "2G", "64K" or "1000" into a byte count.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch strings.ToUpper(s[len(s)-1:]) {
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid -size %q (want e.g. 500M or 2G)", s)
	}
	return n * mult, nil
}
