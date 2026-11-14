package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var runCfg = configFlags{groupField: "region", valueField: "latency_ms"}

// writeFile creates a file in dir with the given content and returns its
// path. t.Helper makes a failure point at the caller's line.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runResult holds everything run produced, so each test can check the parts
// it cares about.
type runResult struct {
	ok             bool
	stdout, stderr string
}

func runWith(paths []string, cfg configFlags, stdin string) runResult {
	var stdout, stderr bytes.Buffer
	ok := run(paths, cfg, strings.NewReader(stdin), &stdout, &stderr)
	return runResult{ok: ok, stdout: stdout.String(), stderr: stderr.String()}
}

func TestRun(t *testing.T) {
	// t.TempDir is removed automatically when the test finishes.
	dir := t.TempDir()
	a := writeFile(t, dir, "a.ndjson", `{"region":"us-east","latency_ms":10}`+"\n"+`{"region":"eu-west","latency_ms":20}`+"\n")
	// No trailing newline: its last line must not join b's first line.
	noNewline := writeFile(t, dir, "nonl.ndjson", `{"region":"us-east","latency_ms":10}`)
	b := writeFile(t, dir, "b.ndjson", `{"region":"us-east","latency_ms":30}`+"\n"+`not json`+"\n")
	missing := filepath.Join(dir, "missing.ndjson")

	strict := runCfg
	strict.strict = true

	tests := []struct {
		name       string
		paths      []string
		cfg        configFlags
		stdin      string
		wantFail   bool     // run should report failure (ok == false)
		wantStdout []string // substrings expected in stdout
		wantStderr []string // substrings expected in stderr
		wantEmpty  bool     // stdout must be empty (no table printed)
	}{
		{
			name:       "no files reads stdin",
			stdin:      `{"region":"ap-south","latency_ms":5}` + "\n" + `oops` + "\n",
			cfg:        runCfg,
			wantStdout: []string{"ap-south", "Total lines processed: 2"},
			wantStderr: []string{"first at line 2:"}, // unnamed stdin: no file name
		},
		{
			name:       "two files merge into one table",
			paths:      []string{a, b},
			cfg:        runCfg,
			wantStdout: []string{"us-east            2       2", "eu-west            1", "Total lines processed: 4"},
		},
		{
			name:       "file without trailing newline doesn't join the next file",
			paths:      []string{noNewline, b},
			cfg:        runCfg,
			wantStdout: []string{"us-east            2       2", "Lines skipped: 1"},
		},
		{
			name:       "line numbers restart per file and name the file",
			paths:      []string{a, b},
			cfg:        runCfg,
			wantStderr: []string{"first at " + b + ":2:"},
		},
		{
			name:       "missing file is reported, others still processed, run fails",
			paths:      []string{a, missing, b},
			cfg:        runCfg,
			wantFail:   true,
			wantStdout: []string{"Total lines processed: 4"},
			wantStderr: []string{"ndstats: open " + missing, "no such file or directory"},
		},
		{
			name:       "missing file with -strict stops before printing",
			paths:      []string{a, missing, b},
			cfg:        strict,
			wantFail:   true,
			wantStderr: []string{"ndstats: open " + missing},
			wantEmpty:  true,
		},
		{
			name:       "bad line with -strict names file and line",
			paths:      []string{a, b},
			cfg:        strict,
			wantFail:   true,
			wantStderr: []string{"ndstats: " + b + ":2: invalid JSON"},
			wantEmpty:  true,
		},
		{
			name:       "dash reads stdin between files",
			paths:      []string{a, "-"},
			cfg:        runCfg,
			stdin:      `{"region":"ap-south","latency_ms":5}` + "\n" + `{"latency_ms":1}` + "\n",
			wantStdout: []string{"ap-south", "Total lines processed: 4"},
			wantStderr: []string{"first at <stdin>:2:"},
		},
		{
			name:       "directory is reported like any unreadable file",
			paths:      []string{dir, a},
			cfg:        runCfg,
			wantFail:   true,
			wantStdout: []string{"Total lines processed: 2"},
			wantStderr: []string{"is a directory"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runWith(tc.paths, tc.cfg, tc.stdin)

			if got.ok == tc.wantFail {
				t.Errorf("run() ok = %v; want %v\nstderr:\n%s", got.ok, !tc.wantFail, got.stderr)
			}
			if tc.wantEmpty && got.stdout != "" {
				t.Errorf("stdout should be empty, got:\n%s", got.stdout)
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("stdout missing %q\nstdout:\n%s", want, got.stdout)
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr missing %q\nstderr:\n%s", want, got.stderr)
				}
			}
		})
	}
}

func TestAddFileMissing(t *testing.T) {
	a := newAggregator(runCfg)
	err := a.addFile(filepath.Join(t.TempDir(), "nope.ndjson"), strings.NewReader(""))
	// os.Open returns an *fs.PathError that wraps fs.ErrNotExist.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("addFile error = %v; want errors.Is(err, fs.ErrNotExist)", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || !strings.HasSuffix(pathErr.Path, "nope.ndjson") {
		t.Errorf("addFile error = %v; want an *fs.PathError naming the file", err)
	}
}

func TestAggregatorAccumulates(t *testing.T) {
	a := newAggregator(runCfg)
	if err := a.add(strings.NewReader(`{"region":"x","latency_ms":1}`+"\n"), "first"); err != nil {
		t.Fatal(err)
	}
	if err := a.add(strings.NewReader(`{"region":"x","latency_ms":3}`+"\n"+`bad`+"\n"), "second"); err != nil {
		t.Fatal(err)
	}

	g := a.groups["x"]
	if g == nil || g.count != 2 || g.min != 1 || g.max != 3 {
		t.Fatalf(`groups["x"] = %+v; want count 2, min 1, max 3`, g)
	}
	if a.t.lines != 3 || a.t.skipped != 1 {
		t.Errorf("totals = lines %d, skipped %d; want 3, 1", a.t.lines, a.t.skipped)
	}
	// The bad line is line 2 of "second", not line 3 overall.
	if r := a.t.reasons[errBadJSON]; r == nil || r.firstFile != "second" || r.firstLine != 2 {
		t.Errorf("reasons[errBadJSON] = %+v; want first at second:2", r)
	}
}

func TestPrintError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "plain error", err: errors.New("open x.ndjson: no such file or directory"), want: "ndstats: open x.ndjson: no such file or directory\n"},
		{name: "too-long line gets a hint", err: fmt.Errorf("reading a.ndjson after line 3: %w", bufio.ErrTooLong),
			want: "ndstats: reading a.ndjson after line 3: bufio.Scanner: token too long (lines are limited to 1048576 bytes)\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printError(&buf, tc.err)
			if got := buf.String(); got != tc.want {
				t.Errorf("printError() = %q; want %q", got, tc.want)
			}
		})
	}
}
