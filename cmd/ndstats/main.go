package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

// Sentinel errors: one value per kind of problem. processLine returns them
// (wrapped with details), and callers recognise the kind with errors.Is.
// Their text doubles as the label in the skip summary.
var (
	errBlankLine   = errors.New("blank line")
	errBadJSON     = errors.New("invalid JSON")
	errNoGroupKey  = errors.New("group key not found")
	errBadGroupKey = errors.New("unusable group key")
	errOtherSkip   = errors.New("other error")
)

// skipKinds lists the reasons a line can be skipped, in the order the
// summary prints them. errOtherSkip catches any kind not listed here.
var skipKinds = []error{errBadJSON, errNoGroupKey, errBadGroupKey, errOtherSkip}

type configFlags struct {
	groupField  string
	valueField  string
	strict      bool
	percentiles []float64 // from -p, sorted and without duplicates; nil if not given
}

// skipReason summarises every skipped line of one kind in constant memory:
// a count, plus the line number and error of the first occurrence.
type skipReason struct {
	count     int
	firstFile string // "" when reading stdin with no file arguments
	firstLine int
	firstErr  error
}

// totals counts input lines across all groups.
type totals struct {
	lines   int                   // every line read, including blank and malformed ones
	skipped int                   // lines that never reached a group
	reasons map[error]*skipReason // keyed by sentinel (one of skipKinds)
}

// recordSkip counts a skipped line under its kind. Only the first line and
// error of each kind are kept, so memory doesn't grow with bad lines.
func (t *totals) recordSkip(file string, line int, err error) {
	if t.reasons == nil {
		t.reasons = make(map[error]*skipReason)
	}
	kind := kindOf(err)
	r, ok := t.reasons[kind]
	if !ok {
		r = &skipReason{firstFile: file, firstLine: line, firstErr: err}
		t.reasons[kind] = r
	}
	r.count++
	t.skipped++
}

type groupStat struct {
	count        int     // records in this group
	skippedCount int     // records whose -field value was missing or unusable
	valueCount   int     // records with a usable -field value
	sum          float64 // running total of values, for the mean
	min          float64 // only valid when valueCount > 0
	max          float64 // only valid when valueCount > 0
	values       *sketch // for percentiles; nil unless -p was given, so groups cost nothing extra without it
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
	if g.values != nil {
		g.values.add(v)
	}
}

func (g *groupStat) mean() float64 {
	return g.sum / float64(g.valueCount)
}

// percentile estimates the p-th percentile (0..100) of the group's values.
// p0 and p100 are the exact min and max, and other estimates are kept
// within that range, so nothing outside the observed values is reported.
func (g *groupStat) percentile(p float64) float64 {
	if g.values == nil {
		return math.NaN() // percentiles weren't requested for this run
	}
	switch p {
	case 0:
		return g.min
	case 100:
		return g.max
	}
	return min(max(g.values.quantile(p/100), g.min), g.max)
}

// aggregator accumulates stats across any number of inputs, so several files
// merge into one table. Each call to add reads one input.
type aggregator struct {
	cfg    configFlags
	groups map[string]*groupStat
	t      totals
}

func newAggregator(cfg configFlags) *aggregator {
	return &aggregator{cfg: cfg, groups: make(map[string]*groupStat)}
}

// add reads NDJSON from r line by line and adds it to the running stats.
// name labels the input in messages ("" for unnamed stdin). Line numbers
// restart at 1 for each input; a.t.lines keeps the overall total.
func (a *aggregator) add(r io.Reader, name string) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	line := 0
	for scanner.Scan() {
		line++
		a.t.lines++
		err := processLine(a.groups, scanner.Text(), a.cfg)
		switch {
		case err == nil, errors.Is(err, errBlankLine):
			// A good line, or a blank one: nothing to report in either mode.
		case a.cfg.strict:
			// Stop at the first bad line. %w keeps err in the chain, so the
			// caller can still ask errors.Is(err, errBadJSON).
			return fmt.Errorf("%s: %w", location(name, line), err)
		default:
			// Skip the line, keep going, and count it by kind. The summary
			// is printed once at the end instead of a log line per skip.
			a.t.recordSkip(name, line, err)
		}
	}

	if err := scanner.Err(); err != nil {
		if line == 0 {
			return err // nothing read yet; the OS error already names the file
		}
		input := name
		if input == "" {
			input = "input"
		}
		return fmt.Errorf("reading %s after line %d: %w", input, line, err)
	}
	return nil
}

// addFile opens path and adds it. "-" means stdin, as in cat and grep, and
// "" means stdin without a name (no file arguments were given), so its
// messages say "line N" instead of "<stdin>:N".
// Each file is opened and closed inside its own call, so the deferred Close
// runs as soon as this file is done, not when the whole program finishes.
func (a *aggregator) addFile(path string, stdin io.Reader) error {
	switch path {
	case "":
		return a.add(stdin, "")
	case "-":
		return a.add(stdin, "<stdin>")
	}
	f, err := os.Open(path)
	if err != nil {
		return err // already says which file: "open x.ndjson: no such file or directory"
	}
	defer f.Close()
	return a.add(f, path)
}

// parseConfig reads the command-line flags. It returns an error instead of
// exiting, so main decides what happens on bad usage.
func parseConfig() (configFlags, error) {
	group := flag.String("group", "", "field to group records by")
	field := flag.String("field", "", "field to calculate statistics for")
	strict := flag.Bool("strict", false, "stop at the first line that can't be assigned to a group, or the first file that can't be read")
	pList := flag.String("p", "", "comma-separated percentiles to report, e.g. 50,95,99 or 99.9 (estimated within 1%)")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "usage: ndstats -group <field> -field <field> [-p 50,95,99] [-strict] [file ...]")
		fmt.Fprintln(out, "Reads NDJSON from the files, or from stdin if none are given (\"-\" also means stdin).")
		flag.PrintDefaults()
	}
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

	percentiles, err := parsePercentiles(*pList)
	if err != nil {
		return configFlags{}, err
	}

	return configFlags{groupField: *group, valueField: *field, strict: *strict, percentiles: percentiles}, nil
}

// parsePercentiles turns "95, 50,99.9" into [50 95 99.9]: each value must be
// a number from 0 to 100. The result is sorted and has no duplicates, so
// the columns come out in order. "" gives nil (no percentile columns).
func parsePercentiles(s string) ([]float64, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var ps []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		p, err := strconv.ParseFloat(part, 64)
		// !(p >= 0 && p <= 100) also rejects NaN, which fails every comparison.
		if err != nil || !(p >= 0 && p <= 100) {
			return nil, fmt.Errorf("invalid -p value %q: want numbers from 0 to 100, e.g. -p 50,95,99", part)
		}
		ps = append(ps, p)
	}
	slices.Sort(ps)
	// Compact removes adjacent duplicates in place and returns the shorter
	// slice; the result must be used, as with append.
	return slices.Compact(ps), nil
}

// percentileLabel formats a percentile as a column heading: 50 -> "p50",
// 99.9 -> "p99.9".
func percentileLabel(p float64) string {
	return "p" + strconv.FormatFloat(p, 'f', -1, 64)
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

func toString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

// groupKeyOf returns the record's group key as a string. Numbers are
// formatted so that 200 and "200" land in the same group.
func groupKeyOf(record map[string]any, field string) (string, error) {
	raw, ok := record[field]
	if !ok {
		return "", fmt.Errorf("%w: %q", errNoGroupKey, field)
	}
	key, ok := toString(raw)
	if !ok {
		return "", fmt.Errorf("%w: %q has type %T", errBadGroupKey, field, raw)
	}
	if key == "" {
		return "", fmt.Errorf("%w: %q is empty", errBadGroupKey, field)
	}
	return key, nil
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

// processLine adds one NDJSON line to groups. It returns an error when the
// line can't be assigned to a group; a missing or unusable -field value is
// not an error, it is counted on the group instead.
func processLine(groups map[string]*groupStat, line string, cfg configFlags) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return errBlankLine
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return fmt.Errorf("%w: %w", errBadJSON, err)
	}

	key, err := groupKeyOf(record, cfg.groupField)
	if err != nil {
		return err
	}

	stat := handleGroupStat(groups, key)
	if stat.values == nil && len(cfg.percentiles) > 0 {
		// About 5 KB per group, so only pay for it when -p asks for it.
		stat.values = &sketch{}
	}
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

// aggregate reads a single unnamed input. It is a convenience wrapper over
// aggregator for one reader.
func aggregate(r io.Reader, cfg configFlags) (map[string]*groupStat, totals, error) {
	a := newAggregator(cfg)
	if err := a.add(r, ""); err != nil {
		return nil, a.t, err
	}
	return a.groups, a.t, nil
}

// kindOf returns which sentinel in skipKinds err wraps, or errOtherSkip.
func kindOf(err error) error {
	for _, kind := range skipKinds {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return errOtherSkip
}

// location formats where a line is, the way compilers and grep -n do:
// "a.ndjson:9" for a named input, or "line 9" for unnamed stdin.
func location(file string, line int) string {
	if file == "" {
		return fmt.Sprintf("line %d", line)
	}
	return fmt.Sprintf("%s:%d", file, line)
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
func printFinalStats(w io.Writer, groups map[string]*groupStat, groupField string, percentiles []float64, t totals) {
	fmt.Fprintf(w, "%-12s %7s %7s %8s %10s %10s %10s", groupField, "count", "values", "skipped", "min", "max", "mean")
	for _, p := range percentiles {
		fmt.Fprintf(w, " %10s", percentileLabel(p))
	}
	fmt.Fprintln(w)

	for _, key := range normaliseGroupKeys(groups) {
		stat := groups[key]
		fmt.Fprintf(w, "%-12s %7d %7d %8d", key, stat.count, stat.valueCount, stat.skippedCount)
		if stat.valueCount == 0 {
			// No usable values: min, max, mean and percentiles are
			// undefined, not 0.
			for range 3 + len(percentiles) {
				fmt.Fprintf(w, " %10s", "-")
			}
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, " %10.2f %10.2f %10.2f", stat.min, stat.max, stat.mean())
		for _, p := range percentiles {
			fmt.Fprintf(w, " %10.2f", stat.percentile(p))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Total lines processed: %d\n", t.lines)
	fmt.Fprintf(w, "Lines skipped: %d\n", t.skipped)
}

// printSkipSummary writes one line per kind of skipped line, in skipKinds
// order, and nothing at all if no line was skipped.
func printSkipSummary(w io.Writer, t totals) {
	if t.skipped == 0 {
		return
	}
	fmt.Fprintf(w, "skipped %d of %d lines:\n", t.skipped, t.lines)
	for _, kind := range skipKinds {
		r, ok := t.reasons[kind]
		if !ok {
			continue
		}
		// kind.Error() is the sentinel's own text, e.g. "invalid JSON".
		// firstErr starts with that same text (it was wrapped as
		// "%w: details"), so trim it to avoid printing the label twice.
		detail := strings.TrimPrefix(r.firstErr.Error(), kind.Error()+": ")
		fmt.Fprintf(w, "  %s: %d (first at %s: %s)\n", kind.Error(), r.count, location(r.firstFile, r.firstLine), detail)
	}
}

// printError writes err to w as "ndstats: <message>", adding a hint to
// errors a user can act on.
func printError(w io.Writer, err error) {
	msg := err.Error()
	if errors.Is(err, bufio.ErrTooLong) {
		msg += fmt.Sprintf(" (lines are limited to %d bytes)", maxLineSize)
	}
	fmt.Fprintf(w, "ndstats: %s\n", msg)
}

// run reads every input, prints the table and the skip summary, and reports
// whether all inputs were read cleanly. It takes its inputs and outputs as
// parameters so tests can call it directly; what to do about failure (such
// as the process exit code) is left to the caller.
func run(paths []string, cfg configFlags, stdin io.Reader, stdout, stderr io.Writer) (ok bool) {
	if len(paths) == 0 {
		paths = []string{""} // no file arguments: read stdin
	}

	a := newAggregator(cfg)
	ok = true
	for _, path := range paths {
		err := a.addFile(path, stdin)
		if err == nil {
			continue
		}
		// Every failed input is reported, in either mode.
		printError(stderr, err)
		ok = false
		// -strict stops at the first failure; otherwise carry on with the
		// next input, like cat and grep.
		if cfg.strict {
			return false
		}
	}

	printFinalStats(stdout, a.groups, cfg.groupField, cfg.percentiles, a.t)
	printSkipSummary(stderr, a.t)
	return ok
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2) // usage error
	}
	// flag.Args() is whatever is left after the flags: the file names.
	if !run(flag.Args(), cfg, os.Stdin, os.Stdout, os.Stderr) {
		os.Exit(1) // an input failed, or -strict stopped at a bad line
	}
}
