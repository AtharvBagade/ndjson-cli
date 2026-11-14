package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

// Go's test runner finds any function named TestXxx that takes *testing.T.
func TestToFloat64(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   float64
		wantOK bool
	}{
		{name: "json number", input: 12.4, want: 12.4, wantOK: true},
		{name: "zero is a valid value", input: 0.0, want: 0, wantOK: true},
		{name: "negative number", input: -3.5, want: -3.5, wantOK: true},
		{name: "numeric string", input: "45", want: 45, wantOK: true},
		{name: "decimal string", input: "9.7", want: 9.7, wantOK: true},
		{name: "exponent string", input: "1e3", want: 1000, wantOK: true},
		{name: "non-numeric string", input: "fast", want: 0, wantOK: false},
		{name: "empty string", input: "", want: 0, wantOK: false},
		{name: "NaN string", input: "NaN", want: 0, wantOK: false},
		{name: "lowercase nan string", input: "nan", want: 0, wantOK: false},
		{name: "Inf string", input: "Inf", want: 0, wantOK: false},
		{name: "negative infinity string", input: "-infinity", want: 0, wantOK: false},
		{name: "out of range string", input: "1e400", want: 0, wantOK: false},
		{name: "leading space", input: " 7.5", want: 7.5, wantOK: true},
		{name: "trailing space", input: "7.5 ", want: 7.5, wantOK: true},
		{name: "tabs and newlines", input: "\t45\n", want: 45, wantOK: true},
		{name: "only whitespace", input: "   ", want: 0, wantOK: false},
		{name: "padded NaN", input: " NaN ", want: 0, wantOK: false},
		{name: "nil", input: nil, want: 0, wantOK: false},
		{name: "bool", input: true, want: 0, wantOK: false},
		{name: "object", input: map[string]any{"a": 1.0}, want: 0, wantOK: false},
		{name: "array", input: []any{1.0}, want: 0, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("toFloat64(%#v) = %v, %v; want %v, %v",
					tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   string
		wantOK bool
	}{
		{name: "string", input: "us-east", want: "us-east", wantOK: true},
		{name: "empty string", input: "", want: "", wantOK: true},
		{name: "surrounding spaces trimmed", input: " us-east ", want: "us-east", wantOK: true},
		// toString only converts; groupKeyOf decides that "" isn't a key.
		{name: "only whitespace", input: "   ", want: "", wantOK: true},
		{name: "whole number has no decimals", input: 200.0, want: "200", wantOK: true},
		{name: "decimal keeps its digits", input: 12.5, want: "12.5", wantOK: true},
		{name: "large number has no exponent", input: 1e21, want: "1000000000000000000000", wantOK: true},
		{name: "negative number", input: -1.0, want: "-1", wantOK: true},
		{name: "nil", input: nil, want: "", wantOK: false},
		{name: "bool", input: false, want: "", wantOK: false},
		{name: "object", input: map[string]any{}, want: "", wantOK: false},
		{name: "array", input: []any{}, want: "", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toString(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("toString(%#v) = %q, %v; want %q, %v",
					tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestGroupKeyOf(t *testing.T) {
	tests := []struct {
		name   string
		record map[string]any
		want   string
		wantIs error // sentinel the error must wrap; nil means no error
	}{
		{name: "string key", record: map[string]any{"region": "us-east"}, want: "us-east"},
		{name: "numeric key", record: map[string]any{"region": 200.0}, want: "200"},
		{name: "padded key is trimmed", record: map[string]any{"region": " us-east "}, want: "us-east"},
		{name: "empty key", record: map[string]any{"region": ""}, wantIs: errBadGroupKey},
		{name: "whitespace-only key", record: map[string]any{"region": "   "}, wantIs: errBadGroupKey},
		{name: "missing key", record: map[string]any{"other": "x"}, wantIs: errNoGroupKey},
		{name: "null key", record: map[string]any{"region": nil}, wantIs: errBadGroupKey},
		{name: "bool key", record: map[string]any{"region": true}, wantIs: errBadGroupKey},
		{name: "object key", record: map[string]any{"region": map[string]any{}}, wantIs: errBadGroupKey},
		// json.Unmarshal of the line `null` gives a nil map; reading from a
		// nil map is allowed and behaves like a missing key.
		{name: "nil record", record: nil, wantIs: errNoGroupKey},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := groupKeyOf(tc.record, "region")
			if tc.wantIs == nil {
				if err != nil {
					t.Fatalf("groupKeyOf(%v) unexpected error: %v", tc.record, err)
				}
			} else if !errors.Is(err, tc.wantIs) {
				t.Fatalf("groupKeyOf(%v) error = %v; want errors.Is(err, %v)", tc.record, err, tc.wantIs)
			}
			if got != tc.want {
				t.Errorf("groupKeyOf(%v) = %q; want %q", tc.record, got, tc.want)
			}
		})
	}
}

func TestLocation(t *testing.T) {
	if got := location("", 9); got != "line 9" {
		t.Errorf(`location("", 9) = %q; want "line 9"`, got)
	}
	if got := location("a.ndjson", 9); got != "a.ndjson:9" {
		t.Errorf(`location("a.ndjson", 9) = %q; want "a.ndjson:9"`, got)
	}
}

func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "sentinel itself", err: errBadJSON, want: errBadJSON},
		{name: "wrapped once", err: fmt.Errorf("%w: %q", errNoGroupKey, "region"), want: errNoGroupKey},
		{name: "wrapped twice", err: fmt.Errorf("line 3: %w", fmt.Errorf("%w: x", errBadGroupKey)), want: errBadGroupKey},
		{name: "unknown error", err: errors.New("disk on fire"), want: errOtherSkip},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindOf(tc.err); got != tc.want {
				t.Errorf("kindOf(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRecordSkip(t *testing.T) {
	var tot totals // zero value: reasons map is nil until the first skip
	tot.recordSkip("a.ndjson", 2, fmt.Errorf("%w: first", errBadJSON))
	tot.recordSkip("b.ndjson", 5, fmt.Errorf("%w: second", errBadJSON))
	tot.recordSkip("", 7, fmt.Errorf("%w: %q", errNoGroupKey, "region"))

	if tot.skipped != 3 {
		t.Errorf("skipped = %d; want 3", tot.skipped)
	}
	bad := tot.reasons[errBadJSON]
	if bad == nil || bad.count != 2 || bad.firstFile != "a.ndjson" || bad.firstLine != 2 {
		t.Fatalf("reasons[errBadJSON] = %+v; want count 2, first at a.ndjson:2", bad)
	}
	// Only the first error of each kind is kept.
	if !strings.Contains(bad.firstErr.Error(), "first") {
		t.Errorf("firstErr = %v; want the first error, not a later one", bad.firstErr)
	}
	if r := tot.reasons[errNoGroupKey]; r == nil || r.count != 1 || r.firstLine != 7 {
		t.Errorf("reasons[errNoGroupKey] = %+v; want count 1, firstLine 7", r)
	}
	if _, ok := tot.reasons[errBadGroupKey]; ok {
		t.Error("a kind that never happened has an entry")
	}
}

func TestGroupStatCounters(t *testing.T) {
	var g groupStat // the zero value is ready to use
	g.incrementCount()
	g.incrementCount()
	g.incrementSkippedCount()

	if got := g.getGroupStats(); got != 2 {
		t.Errorf("getGroupStats() = %d; want 2", got)
	}
	if g.skippedCount != 1 {
		t.Errorf("skippedCount = %d; want 1", g.skippedCount)
	}
}

func TestGroupStatAddValue(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		wantMin  float64
		wantMax  float64
		wantSum  float64
		wantMean float64
	}{
		// min must come from the data, not from the zero value 0.
		{name: "all positive", values: []float64{12.4, 3.2, 250, 9.7, 980}, wantMin: 3.2, wantMax: 980, wantSum: 1255.3, wantMean: 251.06},
		// max must come from the data, not from the zero value 0.
		{name: "all negative", values: []float64{-5, -1, -9}, wantMin: -9, wantMax: -1, wantSum: -15, wantMean: -5},
		{name: "single value", values: []float64{1200.5}, wantMin: 1200.5, wantMax: 1200.5, wantSum: 1200.5, wantMean: 1200.5},
		{name: "zero is a real value", values: []float64{0, 4}, wantMin: 0, wantMax: 4, wantSum: 4, wantMean: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var g groupStat
			for _, v := range tc.values {
				g.addValue(v)
			}
			if g.valueCount != len(tc.values) {
				t.Errorf("valueCount = %d; want %d", g.valueCount, len(tc.values))
			}
			if g.min != tc.wantMin || g.max != tc.wantMax {
				t.Errorf("min, max = %v, %v; want %v, %v", g.min, g.max, tc.wantMin, tc.wantMax)
			}
			// Sums and means come from float arithmetic, so compare with a
			// tolerance instead of ==.
			if !almostEqual(g.sum, tc.wantSum) {
				t.Errorf("sum = %v; want %v", g.sum, tc.wantSum)
			}
			if !almostEqual(g.mean(), tc.wantMean) {
				t.Errorf("mean() = %v; want %v", g.mean(), tc.wantMean)
			}
		})
	}
}

func TestMeanWithNoValues(t *testing.T) {
	// 0/0 is NaN for floats (not a panic, unlike integers). printFinalStats
	// relies on checking valueCount first so this never reaches the output.
	var g groupStat
	if got := g.mean(); !math.IsNaN(got) {
		t.Errorf("mean() of empty group = %v; want NaN", got)
	}
}

func TestHandleGroupStat(t *testing.T) {
	groups := make(map[string]*groupStat)

	first := handleGroupStat(groups, "us-east")
	if first == nil {
		t.Fatal("handleGroupStat returned nil for a new key")
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d; want 1", len(groups))
	}

	first.incrementCount()
	second := handleGroupStat(groups, "us-east")
	if second != first {
		t.Error("handleGroupStat returned a different pointer for an existing key")
	}
	if second.count != 1 {
		t.Errorf("count = %d; want 1 (existing stats must be kept)", second.count)
	}

	handleGroupStat(groups, "eu-west")
	if len(groups) != 2 {
		t.Errorf("len(groups) = %d; want 2", len(groups))
	}
}

func TestNormaliseGroupKeys(t *testing.T) {
	tests := []struct {
		name   string
		counts map[string]int
		want   []string
	}{
		{name: "highest count first", counts: map[string]int{"a": 1, "b": 3, "c": 2}, want: []string{"b", "c", "a"}},
		{name: "ties broken by key", counts: map[string]int{"500": 2, "404": 2, "200": 7}, want: []string{"200", "404", "500"}},
		{name: "keys compare as strings", counts: map[string]int{"200": 1, "1000": 1}, want: []string{"1000", "200"}},
		{name: "empty", counts: map[string]int{}, want: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groups := make(map[string]*groupStat)
			for k, c := range tc.counts {
				groups[k] = &groupStat{count: c}
			}
			// Map order is random, so run several times: a missing
			// tie-breaker would show up as an occasional failure.
			for range 20 {
				got := normaliseGroupKeys(groups)
				if !slices.Equal(got, tc.want) {
					t.Fatalf("normaliseGroupKeys() = %v; want %v", got, tc.want)
				}
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    configFlags
		wantErr string // substring expected in the error; "" means no error
	}{
		{name: "both flags", args: []string{"-group", "region", "-field", "latency_ms"}, want: configFlags{groupField: "region", valueField: "latency_ms"}},
		{name: "equals syntax", args: []string{"-group=status", "-field=bytes"}, want: configFlags{groupField: "status", valueField: "bytes"}},
		{name: "strict off by default", args: []string{"-group", "a", "-field", "b"}, want: configFlags{groupField: "a", valueField: "b", strict: false}},
		{name: "strict flag", args: []string{"-group", "a", "-field", "b", "-strict"}, want: configFlags{groupField: "a", valueField: "b", strict: true}},
		{name: "missing group", args: []string{"-field", "bytes"}, wantErr: "-group"},
		{name: "missing field", args: []string{"-group", "status"}, wantErr: "-field"},
		{name: "missing both", args: nil, wantErr: "-group, -field"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// parseConfig uses the global flag set and os.Args. Give each
			// case a fresh flag set (defining -group twice on one set
			// panics) and restore both afterwards.
			oldArgs, oldFlags := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
			os.Args = append([]string{"ndstats"}, tc.args...)
			flag.CommandLine = flag.NewFlagSet("ndstats", flag.ContinueOnError)

			got, err := parseConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseConfig() error = %v; want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseConfig() = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// almostEqual compares floats that come from arithmetic, where the last
// digit can differ (e.g. 1255.3/5 is not exactly 251.06).
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
