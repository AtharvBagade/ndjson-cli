package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
)

// maxLineSize caps how long a single input line may be. The scanner's buffer
// never grows past this, which keeps memory bounded on any input.
const maxLineSize = 1 << 20 // 1 MB

type configFlags struct {
	groupField string
	valueField string
}

// totals counts input lines across all groups.
type totals struct {
	lines   int // every line read, including blank and malformed ones
	skipped int // lines that never reached a group
}

type groupStat struct {
	count        int     // records in this group
	skippedCount int     // records whose -field value was missing or unusable
	valueCount   int     // records with a usable -field value
	sum          float64 // running total of values, for the mean
	min          float64 // only valid when valueCount > 0
	max          float64 // only valid when valueCount > 0
}

func (g *groupStat) getGroupStats() int {
	return g.count
}

func (g *groupStat) incrementCount() {
	g.count++
}

func (g *groupStat) incrementSkippedCount() {
	g.skippedCount++
}

func (g *groupStat) addValue(v float64) {
	if g.valueCount == 0 || v < g.min {
		g.min = v
	}
	if g.valueCount == 0 || v > g.max {
		g.max = v
	}
	g.sum += v
	g.valueCount++
}

func (g *groupStat) mean() float64 {
	return g.sum / float64(g.valueCount)
}

func normaliseGroupKeys(groups map[string]*groupStat) []string {
	return slices.SortedFunc(maps.Keys(groups), func(a, b string) int {
		countA := groups[a].getGroupStats()
		countB := groups[b].getGroupStats()
		if c := cmp.Compare(countB, countA); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
}

// printFinalStats writes to w rather than stdout so tests can capture the
// output in a bytes.Buffer.
func printFinalStats(w io.Writer, groups map[string]*groupStat, groupField string, t totals) {
	fmt.Fprintf(w, "%-12s %7s %7s %8s %10s %10s %10s\n", groupField, "count", "values", "skipped", "min", "max", "mean")
	for _, key := range normaliseGroupKeys(groups) {
		stat := groups[key]
		if stat.valueCount == 0 {
			// No usable values: min, max and mean are undefined, not 0.
			fmt.Fprintf(w, "%-12s %7d %7d %8d %10s %10s %10s\n", key, stat.count, stat.valueCount, stat.skippedCount, "-", "-", "-")
			continue
		}
		fmt.Fprintf(w, "%-12s %7d %7d %8d %10.2f %10.2f %10.2f\n", key, stat.count, stat.valueCount, stat.skippedCount, stat.min, stat.max, stat.mean())
	}
	fmt.Fprintf(w, "Total lines processed: %d\n", t.lines)
	fmt.Fprintf(w, "Lines skipped: %d\n", t.skipped)
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

func toString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

// parseConfig reads the command-line flags. It returns an error instead of
// exiting, so main decides what happens on bad usage.
func parseConfig() (configFlags, error) {
	group := flag.String("group", "", "field to group records by")
	field := flag.String("field", "", "field to calculate statistics for")
	flag.Parse()

	var missing []string
	if *group == "" {
		missing = append(missing, "-group")
	}
	if *field == "" {
		missing = append(missing, "-field")
	}
	if len(missing) > 0 {
		return configFlags{}, fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}

	return configFlags{groupField: *group, valueField: *field}, nil
}

// handleGroupStat returns the stats for key, adding an empty entry first if the
// group hasn't been seen yet.
func handleGroupStat(groups map[string]*groupStat, key string) *groupStat {
	stat, exists := groups[key]
	if !exists {
		stat = &groupStat{}
		groups[key] = stat
	}
	return stat
}

// groupKeyOf returns the record's group key as a string. Numbers are
// formatted so that 200 and "200" land in the same group.
func groupKeyOf(record map[string]any, field string) (string, error) {
	raw, ok := record[field]
	if !ok {
		return "", fmt.Errorf("group key %q not found", field)
	}
	key, ok := toString(raw)
	if !ok {
		return "", fmt.Errorf("group key %q has unsupported type %T", field, raw)
	}
	return key, nil
}

// processLine adds one NDJSON line to groups. It returns an error when the
// line can't be assigned to a group; a missing or unusable -field value is
// not an error, it is counted on the group instead.
func processLine(groups map[string]*groupStat, line string, cfg configFlags) error {
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	key, err := groupKeyOf(record, cfg.groupField)
	if err != nil {
		return err
	}

	stat := handleGroupStat(groups, key)
	stat.incrementCount()

	// A missing key gives nil, which toFloat64 rejects like any other
	// unusable value. The record still counts toward its group.
	value, ok := toFloat64(record[cfg.valueField])
	if !ok {
		stat.incrementSkippedCount()
		return nil
	}
	stat.addValue(value)
	return nil
}

// aggregate reads NDJSON from r line by line and builds per-group stats.
// Taking an io.Reader means stdin, a file, or a strings.Reader in a test all
// go through the same code.
func aggregate(r io.Reader, cfg configFlags) (map[string]*groupStat, totals, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	groups := make(map[string]*groupStat)
	var t totals

	for scanner.Scan() {
		t.lines++
		if err := processLine(groups, scanner.Text(), cfg); err != nil {
			log.Printf("line %d: %v", t.lines, err)
			t.skipped++
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, t, fmt.Errorf("reading input after line %d: %w", t.lines, err)
	}
	return groups, t, nil
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}

	groups, t, err := aggregate(os.Stdin, cfg)
	if err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			log.Fatalf("%v (lines are limited to %d bytes)", err, maxLineSize)
		}
		log.Fatal(err)
	}

	printFinalStats(os.Stdout, groups, cfg.groupField, t)
}
