package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

var testCfg = configFlags{groupField: "region", valueField: "latency_ms"}

// silenceLog discards the per-line log output from aggregate for the rest of
// the test, so `go test -v` output stays readable.
func silenceLog(t *testing.T) {
	t.Helper()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
}

func TestProcessLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantErr     bool
		wantIs      error  // sentinel the error must match with errors.Is, if any
		wantGroup   string // group expected to exist afterwards ("" if none)
		wantCount   int
		wantValues  int
		wantSkipped int
	}{
		{name: "number value", line: `{"region":"us-east","latency_ms":12.4}`, wantGroup: "us-east", wantCount: 1, wantValues: 1},
		{name: "numeric string value", line: `{"region":"eu-west","latency_ms":"45"}`, wantGroup: "eu-west", wantCount: 1, wantValues: 1},
		{name: "numeric group key", line: `{"region":200,"latency_ms":1}`, wantGroup: "200", wantCount: 1, wantValues: 1},
		{name: "missing value", line: `{"region":"eu-west"}`, wantGroup: "eu-west", wantCount: 1, wantSkipped: 1},
		{name: "null value", line: `{"region":"us-east","latency_ms":null}`, wantGroup: "us-east", wantCount: 1, wantSkipped: 1},
		{name: "non-numeric value", line: `{"region":"ap-south","latency_ms":"fast"}`, wantGroup: "ap-south", wantCount: 1, wantSkipped: 1},
		{name: "NaN value", line: `{"region":"us-east","latency_ms":"NaN"}`, wantGroup: "us-east", wantCount: 1, wantSkipped: 1},
		{name: "bool value", line: `{"region":"eu-west","latency_ms":true}`, wantGroup: "eu-west", wantCount: 1, wantSkipped: 1},
		{name: "extra nested fields are ignored", line: `{"region":"us-east","latency_ms":980,"meta":{"host":"web-3"}}`, wantGroup: "us-east", wantCount: 1, wantValues: 1},
		{name: "padded line", line: "  {\"region\":\"us-east\",\"latency_ms\":1}\t", wantGroup: "us-east", wantCount: 1, wantValues: 1},
		{name: "padded group key", line: `{"region":" us-east ","latency_ms":1}`, wantGroup: "us-east", wantCount: 1, wantValues: 1},
		{name: "padded numeric string value", line: `{"region":"ap-south","latency_ms":" 7.5"}`, wantGroup: "ap-south", wantCount: 1, wantValues: 1},
		{name: "truncated JSON", line: `{"region":"us-east","latency_ms":45`, wantErr: true, wantIs: errBadJSON},
		{name: "array instead of object", line: `[1,2,3]`, wantErr: true, wantIs: errBadJSON},
		{name: "blank line", line: ``, wantErr: true, wantIs: errBlankLine},
		{name: "whitespace-only line", line: " \t ", wantErr: true, wantIs: errBlankLine},
		{name: "JSON null line", line: `null`, wantErr: true},
		{name: "missing group key", line: `{"latency_ms":5}`, wantErr: true},
		{name: "unsupported group key type", line: `{"region":true,"latency_ms":5}`, wantErr: true},
		{name: "whitespace-only group key", line: `{"region":"   ","latency_ms":5}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groups := make(map[string]*groupStat)
			err := processLine(groups, tc.line, testCfg)

			if (err != nil) != tc.wantErr {
				t.Fatalf("processLine(%q) error = %v; wantErr %v", tc.line, err, tc.wantErr)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("processLine(%q) error = %v; want errors.Is(err, %v)", tc.line, err, tc.wantIs)
			}
			if tc.wantErr {
				if len(groups) != 0 {
					t.Errorf("a rejected line created groups: %v", groups)
				}
				return
			}

			stat, ok := groups[tc.wantGroup]
			if !ok {
				t.Fatalf("group %q not created; groups = %v", tc.wantGroup, groups)
			}
			if stat.count != tc.wantCount || stat.valueCount != tc.wantValues || stat.skippedCount != tc.wantSkipped {
				t.Errorf("count, values, skipped = %d, %d, %d; want %d, %d, %d",
					stat.count, stat.valueCount, stat.skippedCount,
					tc.wantCount, tc.wantValues, tc.wantSkipped)
			}
		})
	}
}

func TestProcessLineWrapsJSONError(t *testing.T) {
	err := processLine(make(map[string]*groupStat), `{"region":`, testCfg)
	if err == nil {
		t.Fatal("expected an error for truncated JSON")
	}
	// fmt.Errorf("%w: %w", errBadJSON, err) keeps BOTH errors in the chain.
	// errors.Unwrap can't be used here: it returns nil for an error wrapping
	// more than one error. errors.Is and errors.As search the whole tree.
	if !errors.Is(err, errBadJSON) {
		t.Errorf("error = %v; want errors.Is(err, errBadJSON)", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("error = %v; want the original *json.SyntaxError kept with %%w", err)
	}
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("error = %q; want the JSON parser's message kept", err)
	}
}

func TestAggregate(t *testing.T) {
	type want struct {
		count, values, skipped int
		min, max, mean         float64
	}
	tests := []struct {
		name        string
		input       string
		wantLines   int
		wantSkipped int
		wantGroups  map[string]want
	}{
		{
			name:       "empty input",
			input:      "",
			wantGroups: map[string]want{},
		},
		{
			name:       "no trailing newline",
			input:      `{"region":"a","latency_ms":1}` + "\n" + `{"region":"a","latency_ms":3}`,
			wantLines:  2,
			wantGroups: map[string]want{"a": {count: 2, values: 2, min: 1, max: 3, mean: 2}},
		},
		{
			name: "mixed good and bad lines",
			input: strings.Join([]string{
				`{"region":"us-east","latency_ms":10}`,
				`not json`,
				``,
				`{"region":"us-east","latency_ms":"30"}`,
				`{"region":"eu-west","latency_ms":"fast"}`,
				`{"latency_ms":5}`,
			}, "\n"),
			wantLines:   6,
			wantSkipped: 2, // "not json" and the missing group key; blank lines aren't counted
			wantGroups: map[string]want{
				"us-east": {count: 2, values: 2, min: 10, max: 30, mean: 20},
				"eu-west": {count: 1, skipped: 1},
			},
		},
		{
			name:       "only blank lines",
			input:      "\n   \n\t\n",
			wantLines:  3,
			wantGroups: map[string]want{},
		},
		{
			name: "every kind of bad line is counted",
			input: strings.Join([]string{
				`not json`,
				`[1,2,3]`,
				`{"latency_ms":5}`,
				`{"region":true}`,
				`{"region":"   "}`,
			}, "\n"),
			wantLines:   5,
			wantSkipped: 5,
			wantGroups:  map[string]want{},
		},
		{
			name: "padded keys merge",
			input: `{"region":"us-east","latency_ms":2}` + "\n" +
				`{"region":" us-east ","latency_ms":4}` + "\n",
			wantLines:  2,
			wantGroups: map[string]want{"us-east": {count: 2, values: 2, min: 2, max: 4, mean: 3}},
		},
		{
			name: "200 and \"200\" are one group",
			input: `{"region":200,"latency_ms":1}` + "\n" +
				`{"region":"200","latency_ms":2}` + "\n",
			wantLines:  2,
			wantGroups: map[string]want{"200": {count: 2, values: 2, min: 1, max: 2, mean: 1.5}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			silenceLog(t)

			groups, tot, err := aggregate(strings.NewReader(tc.input), testCfg)
			if err != nil {
				t.Fatalf("aggregate() unexpected error: %v", err)
			}
			if tot.lines != tc.wantLines || tot.skipped != tc.wantSkipped {
				t.Errorf("totals = %+v; want lines %d, skipped %d", tot, tc.wantLines, tc.wantSkipped)
			}
			if len(groups) != len(tc.wantGroups) {
				t.Fatalf("got %d groups %v; want %d", len(groups), groups, len(tc.wantGroups))
			}
			for key, w := range tc.wantGroups {
				g, ok := groups[key]
				if !ok {
					t.Errorf("group %q missing", key)
					continue
				}
				if g.count != w.count || g.valueCount != w.values || g.skippedCount != w.skipped {
					t.Errorf("group %q: count, values, skipped = %d, %d, %d; want %d, %d, %d",
						key, g.count, g.valueCount, g.skippedCount, w.count, w.values, w.skipped)
				}
				if w.values > 0 && (g.min != w.min || g.max != w.max || !almostEqual(g.mean(), w.mean)) {
					t.Errorf("group %q: min, max, mean = %v, %v, %v; want %v, %v, %v",
						key, g.min, g.max, g.mean(), w.min, w.max, w.mean)
				}
			}
		})
	}
}

// TestAggregateSmallFile is the end-to-end check against the hand-worked
// expected table for testdata/small.ndjson.
func TestAggregateSmallFile(t *testing.T) {
	silenceLog(t)

	// Tests run with the package directory as the working directory.
	f, err := os.Open("../../testdata/small.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	groups, tot, err := aggregate(f, testCfg)
	if err != nil {
		t.Fatalf("aggregate() unexpected error: %v", err)
	}
	// Line 9 is truncated JSON; line 11 is blank and not counted.
	if tot.lines != 19 || tot.skipped != 1 {
		t.Errorf("totals = %+v; want lines 19, skipped 1", tot)
	}

	want := map[string]struct {
		count, values, skipped int
		min, max, mean         float64
	}{
		"us-east":  {7, 5, 2, 3.2, 980, 251.06},
		"eu-west":  {6, 4, 2, 18.9, 45, 29},
		"ap-south": {4, 2, 2, 7.5, 1200.5, 604}, // " 7.5" counts after trimming
	}
	for key, w := range want {
		g, ok := groups[key]
		if !ok {
			t.Errorf("group %q missing", key)
			continue
		}
		if g.count != w.count || g.valueCount != w.values || g.skippedCount != w.skipped ||
			g.min != w.min || g.max != w.max || !almostEqual(g.mean(), w.mean) {
			t.Errorf("group %q = {count %d, values %d, skipped %d, min %v, max %v, mean %v}; want %+v",
				key, g.count, g.valueCount, g.skippedCount, g.min, g.max, g.mean(), w)
		}
	}
}

func TestAggregateStrict(t *testing.T) {
	strictCfg := testCfg
	strictCfg.strict = true

	tests := []struct {
		name      string
		input     string
		wantLine  string // "line N" expected in the error
		wantIs    error  // sentinel expected in the chain, if any
		wantLines int    // lines read when it stopped
	}{
		{
			name:      "invalid JSON stops the run",
			input:     `{"region":"a","latency_ms":1}` + "\n" + `{"region":` + "\n" + `{"region":"b","latency_ms":2}`,
			wantLine:  "line 2",
			wantIs:    errBadJSON,
			wantLines: 2,
		},
		{
			name:      "missing group key stops the run",
			input:     `{"region":"a","latency_ms":1}` + "\n" + `{"latency_ms":5}`,
			wantLine:  "line 2",
			wantLines: 2,
		},
		{
			name:      "line numbers count blank lines",
			input:     "\n\n" + `not json`,
			wantLine:  "line 3",
			wantIs:    errBadJSON,
			wantLines: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			silenceLog(t)

			_, tot, err := aggregate(strings.NewReader(tc.input), strictCfg)
			if err == nil {
				t.Fatal("aggregate() in strict mode returned no error for a bad line")
			}
			if !strings.Contains(err.Error(), tc.wantLine) {
				t.Errorf("error = %q; want it to name %q", err, tc.wantLine)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("error = %v; want errors.Is(err, %v)", err, tc.wantIs)
			}
			if tot.lines != tc.wantLines {
				t.Errorf("lines read = %d; want %d (should stop at the bad line)", tot.lines, tc.wantLines)
			}
		})
	}
}

func TestAggregateStrictAcceptsGoodInput(t *testing.T) {
	strictCfg := testCfg
	strictCfg.strict = true

	// Blank lines and unusable -field values are not errors, even in strict
	// mode: the line still belongs to a group.
	input := `{"region":"a","latency_ms":1}` + "\n\n" + `{"region":"a","latency_ms":"fast"}` + "\n"
	groups, tot, err := aggregate(strings.NewReader(input), strictCfg)
	if err != nil {
		t.Fatalf("aggregate() unexpected error: %v", err)
	}
	if tot.lines != 3 || tot.skipped != 0 {
		t.Errorf("totals = %+v; want lines 3, skipped 0", tot)
	}
	if g := groups["a"]; g == nil || g.count != 2 || g.valueCount != 1 || g.skippedCount != 1 {
		t.Errorf("group a = %+v; want count 2, values 1, skipped 1", g)
	}
}

func TestAggregateLongLine(t *testing.T) {
	f, err := os.Open("../../testdata/longline.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	groups, tot, err := aggregate(f, testCfg)
	if err != nil {
		t.Fatalf("a line over 64 KB should be accepted, got: %v", err)
	}
	if tot.lines != 4 || groups["us-east"].count != 3 {
		t.Errorf("lines = %d, us-east count = %d; want 4, 3", tot.lines, groups["us-east"].count)
	}
}

func TestAggregateLineTooLong(t *testing.T) {
	input := `{"region":"a","latency_ms":1}` + "\n" + strings.Repeat("x", maxLineSize+1) + "\n"

	_, tot, err := aggregate(strings.NewReader(input), testCfg)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("error = %v; want one wrapping bufio.ErrTooLong", err)
	}
	if tot.lines != 1 {
		t.Errorf("lines read before the error = %d; want 1", tot.lines)
	}
}

func TestPrintFinalStats(t *testing.T) {
	groups := map[string]*groupStat{
		"us-east":  {count: 2, valueCount: 2, sum: 40, min: 10, max: 30},
		"ap-south": {count: 1, skippedCount: 1}, // no usable values
	}

	var buf bytes.Buffer
	printFinalStats(&buf, groups, "region", totals{lines: 4, skipped: 1})

	want := "" +
		"region         count  values  skipped        min        max       mean\n" +
		"us-east            2       2        0      10.00      30.00      20.00\n" +
		"ap-south           1       0        1          -          -          -\n" +
		"Total lines processed: 4\n" +
		"Lines skipped: 1\n"

	if got := buf.String(); got != want {
		t.Errorf("printFinalStats output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}
