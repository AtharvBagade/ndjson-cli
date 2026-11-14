# ndstats

A Go command-line tool that streams newline-delimited JSON (NDJSON), groups records by a field, and reports statistics and percentiles of a numeric field for each group, in constant memory.

```sh
ndstats -group region -field latency_ms -p 50,95,99 access-1.ndjson access-2.ndjson
cat access.ndjson | ndstats -group region -field latency_ms
```

## Goal

Log files and event exports are often NDJSON and often too large to load into memory. `ndstats` answers questions like *"what's the p99 latency per region?"* or *"how big are responses for each status code?"* by reading the input once, line by line, and keeping only a small running summary per group. Memory depends on the number of groups, not the size of the file.

The project is also a deliberate exercise in idiomatic Go: standard library only, streaming I/O, errors as values, and small functions that can be tested.

## Status

**Complete.** Every goal of the project is met: streaming in constant memory (measured), table-driven tests passing with `-race`, and `go vet` clean. Possible extensions are listed under [Limitations and future ideas](#limitations-and-future-ideas).

| Area | State |
|---|---|
| Stream NDJSON from files or stdin | Done |
| `-group`: group by a string or numeric field | Done |
| `-field`: count, min, max, mean per group | Done |
| Bad lines skipped and reported with line numbers | Done |
| `-strict`: stop at the first bad line instead | Done |
| Whitespace trimmed from values and group keys; blank lines ignored | Done |
| Long lines (up to 1 MB) | Done |
| Deterministic, sorted output | Done |
| Code split into testable functions taking `io.Reader` / `io.Writer` | Done |
| One summary of skipped lines per reason, with the first line number | Done |
| File arguments, merged into one table; `-` for stdin; `file:line` in messages | Done |
| Table-driven tests, `go test -race` (175 cases, ~95% coverage) | Done |
| `-p 50,95,99` percentiles, estimated within 1% in constant memory | Done |
| Verified constant memory (measured up to 1 GB) | Done |

## Usage

Requires Go 1.27 or later.

```sh
go build -o ndstats ./cmd/ndstats
# or
go install github.com/AtharvBagade/ndjson-cli/cmd/ndstats@latest
```

| Flag | Default | Description |
|---|---|---|
| `-group` | (required) | Field to group records by |
| `-field` | (required) | Numeric field to compute statistics for |
| `-p` | (none) | Comma-separated percentiles to add as columns, from 0 to 100, e.g. `50,95,99` or `99.9`. Estimated within 1%; see [Percentile method](#percentile-method) |
| `-strict` | `false` | Stop at the first line that can't be assigned to a group, or the first file that can't be read |

```
usage: ndstats -group <field> -field <field> [-p 50,95,99] [-strict] [file ...]
```

Files are read in order and merged into one table. With no files, `ndstats` reads stdin; `-` also means stdin, so it can be mixed with files. Flags must come before file names.

| Exit status | Meaning |
|---|---|
| `0` | Every input was read |
| `1` | An input couldn't be read, or `-strict` stopped at a bad line |
| `2` | Wrong usage, e.g. a missing required flag |

If a required flag is missing, `ndstats` names it and prints usage:

```
$ ndstats -field latency_ms
missing required flag(s): -group
```

### Examples

Percentiles per region. p50 is the typical request, p95/p99 the slow tail; on skewed data like latency, the mean alone hides the tail:

```
$ go run ./testdata/gen -size 500M > /tmp/500m.ndjson
$ ndstats -group region -field latency_ms -p 50,95,99,99.9 /tmp/500m.ndjson 2>/dev/null
region         count  values  skipped        min        max       mean        p50        p95        p99      p99.9
ap-south      497633  497512      121       0.40    1061.60      30.15      20.29      89.13     162.41     333.67
us-east       495885  495769      116       0.20    1783.90      30.14      20.29      89.13     162.41     320.58
eu-west       495719  495606      113       0.30    2366.10      30.14      20.29      87.37     162.41     320.58
ap-northeast  495340  495206      134       0.30    1201.30      30.17      20.29      89.13     162.41     327.06
eu-central    495134  495019      115       0.30    1127.30      30.11      20.29      89.13     162.41     320.58
us-west       494361  494251      110       0.20    1107.70      30.11      20.29      89.13     162.41     320.58
Total lines processed: 2976419
Lines skipped: 1564
```

The mean is about 30 ms in every region, but 1 request in 100 takes over 160 ms. The generator uses a fixed seed, so these numbers are reproducible.

Two files merged into one table. Line numbers restart for each file, so the summary says which file:

```
$ ndstats -group region -field latency_ms access-1.ndjson access-2.ndjson
region         count  values  skipped        min        max       mean
us-east            3       3        0       8.00     250.00      90.00
ap-south           1       1        0      90.00      90.00      90.00
eu-west            1       1        0      30.00      30.00      30.00
Total lines processed: 6
Lines skipped: 1
skipped 1 of 6 lines:
  invalid JSON: 1 (first at access-2.ndjson:3: unexpected end of JSON input)
```

A file that can't be read is reported, and the others are still processed (exit status 1):

```
$ ndstats -group region -field latency_ms access-1.ndjson access-3.ndjson
ndstats: open access-3.ndjson: no such file or directory
region         count  values  skipped        min        max       mean
us-east            2       2        0      12.00     250.00     131.00
eu-west            1       1        0      30.00      30.00      30.00
Total lines processed: 3
Lines skipped: 0
```

Latency per region, from stdin:

```
$ ndstats -group region -field latency_ms < testdata/small.ndjson
region         count  values  skipped        min        max       mean
us-east            7       5        2       3.20     980.00     251.06
eu-west            6       4        2      18.90      45.00      29.00
ap-south           4       2        2       7.50    1200.50     604.00
Total lines processed: 19
Lines skipped: 1
skipped 1 of 19 lines:
  invalid JSON: 1 (first at line 9: unexpected end of JSON input)
```

Line 9 is truncated JSON, so it's skipped and counted. Line 11 is blank, so it's ignored and not counted as skipped. The last two lines are the skip summary, written to stderr.

A messier input, with one summary line per reason, however many bad lines there are:

```
$ printf '%s\n' '{"region":"a","latency_ms":1}' 'not json' '{"latency_ms":2}' '{"latency_ms":3}' \
    '{"region":true}' '{"region":"   "}' '[1,2]' | ndstats -group region -field latency_ms
region         count  values  skipped        min        max       mean
a                  1       1        0       1.00       1.00       1.00
Total lines processed: 7
Lines skipped: 6
skipped 6 of 7 lines:
  invalid JSON: 2 (first at line 2: invalid character 'o' in literal null (expecting 'u'))
  group key not found: 2 (first at line 3: "region")
  unusable group key: 2 (first at line 5: "region" has type bool)
```

Bytes per status code, with the skip summary hidden:

```
$ ndstats -group status -field bytes < testdata/small.ndjson 2>/dev/null
status         count  values  skipped        min        max       mean
200               11      10        1      64.00    4096.00     916.80
404                3       3        0     128.00     128.00     128.00
500                2       2        0      64.00      90.00      77.00
503                1       1        0       0.00       0.00       0.00
Total lines processed: 19
Lines skipped: 1
```

Strict mode, which stops at the first bad line and prints no table:

```
$ ndstats -group region -field latency_ms -strict < testdata/small.ndjson
ndstats: line 9: invalid JSON: unexpected end of JSON input
$ echo $?
1
```

Columns:

- **count**: records in the group.
- **values**: records with a usable `-field` value.
- **skipped**: records in the group whose `-field` value was missing or unusable.
- **min**, **max**, **mean**: computed exactly over the usable values. They print as `-` when a group has none.
- **pN** (with `-p`): the N-th percentile of the usable values, estimated within 1%. `p0` and `p100` are the exact min and max.

The table goes to stdout and diagnostics go to stderr, so the output can be piped or redirected cleanly.

## Input format

- Each line must be a JSON **object**. Lines can be up to 1 MB long.
- **Blank lines** (empty or whitespace only) are ignored. They still count toward line numbers, so diagnostics match the file.
- **Bad lines**: a line that isn't valid JSON, isn't an object, or has no usable group key is skipped and counted in "Lines skipped". At the end, a summary on stderr gives the count for each reason, with the line number and error of its first occurrence. With `-strict`, the first such line stops the run with exit status 1. Locations are `file:line` (line numbers restart for each file), or `line N` for stdin with no file arguments.
- Fields are addressed by top-level name. Nested fields (e.g. `meta.host`) are not supported yet.
- **Group key** (`-group`): strings are used with surrounding whitespace trimmed, so `" us-east "` and `"us-east"` are one group. Numbers are formatted without trailing zeros, so `200` and `"200"` fall into the same group. A group field that is missing, `null`, empty or whitespace only, a boolean, an object or an array makes the line a bad line.
- **Value** (`-field`): JSON numbers and numeric strings are accepted, with surrounding whitespace trimmed (`"45"`, `" 7.5"`). Missing, `null`, non-numeric strings (`"fast"`), `"NaN"`, `"Inf"` and other types are not used as values. This isn't a bad line, even with `-strict`: the record still counts toward its group, and the value is counted in that group's **skipped** column.

## How it works

```
files / stdin → bufio.Scanner → json.Unmarshal → per-group stats → sorted table
 (io.Reader)     (1 MB cap)       (one line)      (running totals)   (io.Writer)
```

- **`aggregator`** holds the groups and totals for the whole run. **`add(r io.Reader, name)`** reads one input into it: it owns the scanner, the line limit, per-input line numbers and read errors, and decides what happens to a bad line (skip and count it, or, with `-strict`, return it as an error). Because it takes an `io.Reader`, files, stdin and `strings.NewReader` in tests all go through the same code.
- **`addFile(path)`** opens one file (or stdin for `-`), `defer`s its `Close`, and calls `add`. Each file is opened and closed in its own call, so a deferred `Close` runs as soon as that file is done rather than at the end of the program.
- **`processLine`** parses one line, resolves its group key and updates that group. It returns an error only when the line can't be assigned to a group, and it doesn't know about strict mode. Each kind of problem has a sentinel error (`errBlankLine`, `errBadJSON`, `errNoGroupKey`, `errBadGroupKey`), wrapped with details, so `aggregate` can tell them apart with `errors.Is`.
- **`groupStat`** keeps `count`, `valueCount`, `skippedCount`, `sum`, `min` and `max`, and, with `-p`, a percentile **`sketch`** (counts per bucket). Each line updates these in place and is then discarded.
- **`totals`** counts lines and, for each skip reason, a `skipReason` with the count, first line number and first error. Only the first occurrence is kept, so this stays constant-size too.
- **`run`** loops over the inputs, prints any input's error (`printError`), stops early only under `-strict`, then prints the results. It returns `ok bool`; only `main` turns that into an exit code.
- **`printFinalStats(w io.Writer, …)`** sorts the groups and writes the table to stdout. **`printSkipSummary(w io.Writer, …)`** writes the skip summary to stderr, labelling each reason with its sentinel's own text (`kind.Error()`).

**Why memory stays constant:** no record or value is kept after its line is processed. The scanner's buffer is capped at 1 MB (`maxLineSize = 1 << 20`), and each group holds six numbers, plus a fixed-size sketch with `-p`. Memory grows with the number of distinct groups, not with the input size.

Measured with `/usr/bin/time -l` (macOS, Apple Silicon) on files from the generator, grouping into 6 regions:

| Input | Lines | Peak memory (max RSS) | Time |
|---|---|---|---|
| 50 MB | 297,674 | 11.2 MB | 0.40 s |
| 500 MB | 2,976,419 | 11.5 MB | 3.80 s |
| 1 GB | 6,095,675 | 11.9 MB | 7.94 s |

20× the input adds under 1 MB of memory, and time grows linearly (about 130 MB/s, CPU-bound on JSON parsing). With `-p 50,95,99,99.9` the 500 MB run used 12.1 MB and took the same time: the sketch is updated once per value, which costs far less than parsing the JSON line.

Memory grows with the number of **groups**, as expected: 1 million lines over 10,000 groups use 12 MB without `-p` and 67 MB with it, about 5.5 KB per group for the sketch. To reproduce:

```sh
go build -o /tmp/ndstats ./cmd/ndstats
go run ./testdata/gen -size 1G > /tmp/1g.ndjson
/usr/bin/time -l /tmp/ndstats -group region -field latency_ms /tmp/1g.ndjson > /dev/null
```

## Percentile method

**Method:** a log-bucketed histogram, as in [DDSketch](https://arxiv.org/abs/1908.10693) (Masson, Rim & Lee, VLDB 2019), in [`cmd/ndstats/sketch.go`](cmd/ndstats/sketch.go).

**Why not exact:** an exact percentile needs every value kept and sorted. For the 1 GB benchmark file that's 6 million `float64`s (48 MB), growing with the input, which breaks the constant-memory guarantee.

**How it works:** each group counts its values in buckets whose bounds grow by a fixed ratio γ = (1 + α) / (1 − α), with α = 0.01. Bucket *i* holds the values in (γ^(i−1), γ^i]; a value's bucket is `ceil(log(v) / log γ)`. To find a percentile, walk the buckets in order, adding up their counts, until the running count reaches the percentile's rank, and report that bucket's midpoint 2γ^i / (γ + 1). Negative values are counted by magnitude in a second set of buckets, and values with |v| < 10⁻⁹ (including zero) in a zero bucket.

**Accuracy:** every reported percentile is within **±1% of the true value**, relative to its size: p99 = 162.41 means the true p99 is between about 160.8 and 164.0. The bound holds at every percentile, including the tail (p99, p99.9), and at any scale: 0.5 ms and 4 GB get the same guarantee. The tests check this against exact percentiles on log-normal, uniform, zero-heavy, negative, integer and 18-orders-of-magnitude data.

**Definition:** nearest rank, so pN is the smallest value that at least N% of values are ≤. For p99 of 5 values that's the largest value. The other common definition rounds down and would report the 4th, understating the tail. `p0` and `p100` return the exact min and max, and every estimate is kept within them.

**Memory:** a sketch only stores counts for the buckets between its smallest and largest value: a few hundred for typical data (0.2 ms to 2.4 s is under 500 buckets), about 5.5 KB per group. It doesn't grow with the number of values, and it's only created when `-p` is given.

**Alternatives considered:**

| Method | Why not |
|---|---|
| Exact (store and sort) | Memory grows with the input |
| Reservoir sample (keep K random values) | Accurate near the median, but few tail values land in the sample, so p99/p99.9 are noisy; results change between runs unless seeded |
| Fixed-width histogram | Needs the data's range in advance; a general tool can't know whether `-field` is milliseconds or bytes |
| P² (one small estimator per percentile) | No error guarantee, and can be far off on skewed data |
| t-digest, KLL, GK | Comparable or better accuracy, but much more complex to implement and test |

## Design decisions

| Decision | Choice | Why |
|---|---|---|
| Multiple files | Merged into one table; each file read separately | Files like `access-1`, `access-2` are usually one log split into parts. Reading them separately (rather than joining them with `io.MultiReader`) keeps per-file line numbers and stops a file without a trailing newline from joining the next file's first line. |
| Unreadable file | Reported; the other files still run; exit status 1. `-strict` stops | The same convention as `cat` and `grep`: one bad path shouldn't hide the results from the others, but the exit status still signals the failure. |
| Bad lines | Skip and count by default, with one summary per reason; `-strict` stops at the first one | One bad line in a multi-GB log shouldn't lose the whole run, but some pipelines need to fail loudly on bad input. The final count makes skipped lines visible either way. |
| Skip summary | One line per reason with its count and first occurrence, instead of a log line per bad line | On a large, messy file a log line per skip buries the table and still leaves the user counting. Keeping only the first line and error per reason keeps memory constant. |
| Blank lines | Ignored, not counted as skipped | A blank line isn't lost data, so it shouldn't raise the skipped count. Line numbers still include them. |
| Whitespace | Trimmed from values and group keys | `" 7.5"` and `"us-east "` are almost always formatting noise, not different data. A key that is only whitespace is rejected rather than becoming a group named `""`. |
| Error kinds | Sentinel errors (`errBlankLine`, `errBadJSON`) wrapped with `%w` | `aggregate` and `main` can recognise the kind with `errors.Is` while the message keeps the parser's details. |
| Parsing | `map[string]any` instead of a struct | The field names come from flags at runtime, so they can't be known ahead of time to define struct fields. |
| Numeric vs string group keys | Merge `200` and `"200"` | The same field is often a number in some records and a string in others. Users mean the same group. |
| Numeric strings as values | Accepted | Same reason. `"45"` is a latency of 45. |
| `NaN` / `Inf` | Rejected | `strconv.ParseFloat` accepts them, but one NaN would make a group's sum and mean NaN permanently and break min/max comparisons. |
| Unusable value | Record counts, value skipped | Separates "how many requests" from "how many had a measurable latency". |
| Percentiles | DDSketch-style log histogram, 1% relative accuracy | Constant memory, a guaranteed error bound at every percentile and scale, deterministic output for tests, and simple enough to implement and test in full. See [Percentile method](#percentile-method). |
| Percentile definition | Nearest rank; p0/p100 exact | Never understates the tail on small groups, and the extremes are known exactly anyway. |
| Sketch only with `-p` | `groupStat.values` is a `*sketch`, `nil` without `-p` | A sketch is about 5.5 KB per group. With 10,000 groups that's 12 MB vs 67 MB, so runs that don't ask for percentiles shouldn't pay for them. |
| `-p` parsing | Sorted, duplicates removed, 0 to 100, decimals allowed | Columns come out in order whatever order they were typed in; `-p status` or `-p 101` is a usage error (exit 2). |
| Mean | Derived from `sum / valueCount` at print time | It can't go stale, and it costs one division per group instead of one per line. |
| min/max start | Set by the first value | The zero value `0` would be wrong for all-positive data. |
| Output order | Highest count first, ties by key | Go randomises map iteration on purpose, so an explicit, total order keeps output deterministic and testable. |
| Line limit | 1 MB | Large enough for real log lines, small enough that one bad input can't exhaust memory. |
| Required flags | Both `-group` and `-field` | Keeps the output format fixed for now. |
| Errors | Wrapped with `%w`, checked with `errors.Is` | Callers can recognise `bufio.ErrTooLong` under added context and explain the limit. |
| Exit codes | Decided only in `main`; `run` returns `ok` | Exit codes belong to the process. Keeping them out of `run` keeps it testable and free of process details. |

## Testing

```sh
go vet ./...                 # clean
go test ./...                # 175 table-driven cases
go test -race ./...
go test -cover ./cmd/ndstats # ~95% of statements
```

The tests are table-driven and split by topic:

- **`sketch_test.go`** checks the percentile sketch against exact nearest-rank percentiles at 11 quantiles (p0 to p100, including p99.9) on 11 data shapes (log-normal, uniform, zero-heavy, negative, integers, 18 orders of magnitude, single and equal values), and that every estimate is within 1%. It also covers the rank rule, values small enough to count as zero, the bucket store growing at both ends, and bounded bucket counts for a million values.
- **`helpers_test.go`** covers the value-in, value-out functions: `toFloat64` (numbers, numeric and padded strings, `"NaN"`, `"Inf"`, `"1e400"`, wrong types), `toString`, `groupKeyOf`, the `groupStat` methods (including all-negative data and zero as a real value), `groupStat.percentile` (exact p0/p100, nearest rank, NaN without a sketch), `parsePercentiles` (sorting, duplicates, `status`, `101`, `NaN`, empty items), `handleGroupStat`, `kindOf` and `recordSkip` (only the first error per reason is kept), the sort order (repeated runs catch random map order), and `parseConfig` (with a fresh `flag.FlagSet` per case).
- **`aggregate_test.go`** covers `processLine` (each kind of good and bad line, checked with `errors.Is` against the sentinels), error wrapping (`errors.As` finds the original `*json.SyntaxError`), `aggregate` in default and strict mode, an end-to-end run on `testdata/small.ndjson` against a hand-computed table, the 69 KB line in `testdata/longline.ndjson`, a line over the 1 MB limit (`errors.Is(err, bufio.ErrTooLong)`), the exact table (with and without percentile columns) and skip-summary output written to a `bytes.Buffer`, percentiles of `testdata/small.ndjson` against hand-computed values, and that a sketch is only created with `-p`.

`testdata/small.ndjson` has one edge case per line: malformed and blank lines, numbers vs strings, missing, `null`, `"fast"`, `"NaN"`, `"Inf"`, booleans, nested objects and padded numeric strings. `testdata/longline.ndjson` contains a realistic checkout log line with a full order response body, longer than `bufio.Scanner`'s 64 KB default.

**`testdata/gen`** generates large synthetic access logs for measuring memory and speed. It streams its output, so it runs in constant memory too, and the same `-seed` always gives the same file. The Go tool skips directories named `testdata`, so it isn't part of `go build`/`go test ./...`; run it with `go run ./testdata/gen`:

| Flag | Default | Meaning |
|---|---|---|
| `-lines` | 0 | Number of lines to write |
| `-size` | | Stop after about this many bytes (`500M`, `2G`), instead of `-lines` |
| `-groups` | 0 | Use `region-0` … `region-N` instead of six real regions, to test many groups |
| `-bad` | 0.001 | Fraction of bad lines (truncated JSON, missing `region`, `"fast"` latency, blank) |
| `-seed` | 1 | Random seed |

Latencies are log-normal (mostly fast, with a long slow tail), like real traffic.

- **`run_test.go`** calls `run` with real files in `t.TempDir()`, a `strings.Reader` as stdin and `bytes.Buffer`s as stdout and stderr: no files (stdin), two files merged, a file without a trailing newline, per-file line numbers in the summary, a missing file with and without `-strict`, a bad line under `-strict`, `-` between files, and a directory. It also checks that a missing file's error matches `fs.ErrNotExist`, that two inputs add to one aggregator, and the `printError` hints.

`main` itself isn't tested. It only parses flags, calls `run` and picks the exit code.

## What I learned

Go-specific lessons from building this, each found by hitting it in practice:

- **`bufio.Scanner` has a 64 KB line limit by default.** It fails with `token too long`. `Scanner.Buffer` raises the limit, and the limit is also what guarantees bounded memory.
- **`any` is like TypeScript's `unknown`, not its `any`.** You can't index it until you narrow it with a type assertion or a type switch.
- **`encoding/json` decodes every number into `float64`**, so `.(string)` on a numeric field quietly fails.
- **A variable's type never changes.** Assigning a narrowed value back into an `any` wraps it again. A type switch gives you a *new* variable.
- **`:=` declares, `=` assigns.** Inside an `if`, `:=` creates a new variable that hides the outer one, and `:=` reuses existing names when at least one name on the left is new.
- **Map iteration order is random by design.** Sort the keys before output, and use a comparison function that never returns 0 for two different keys.
- **`strconv.ParseFloat("NaN")` succeeds.** NaN and Inf are valid IEEE 754 values, so the program has to reject them itself.
- **Zero values aren't always valid starting values.** `min` starting at `0` is wrong.
- **Errors are values.** The function that finds a problem returns it, and the caller decides what to do. That's how `-strict` was added without changing `processLine`. `errors.Is` finds a sentinel error through the wrapping.
- **Branching on error kinds needs a `default`.** Without one, an error kind nobody checked for is silently dropped. A test caught exactly that.
- **`errors.Unwrap` returns `nil` for an error that wraps two errors** (`fmt.Errorf("%w: %w", a, b)`). `errors.Is` and `errors.As` search the whole chain.
- **A sentinel's text is its label.** `errBadJSON.Error()` is `"invalid JSON"`, so the summary defines each label once. Sentinels also work as map keys, because `errors.New` returns a comparable pointer.
- **Writing to a nil map panics.** A struct's zero value has a `nil` map; reading it is fine, so create it with `make` before the first write.
- **Strings are immutable.** `strings.TrimSpace` returns a new string, and the original is unchanged.
- **Accept interfaces (`io.Reader`, `io.Writer`) at the boundaries** so the same code works with stdin, files and tests. Adding file arguments needed no change to the reading code: `*os.File` is an `io.Reader`.
- **`defer` runs when the function returns, not at the end of a loop iteration.** Opening files in a loop with `defer f.Close()` keeps them all open; one function per file closes each as soon as it's done.
- **Keep `os.Exit` in `main`.** `run` returns `ok` and takes its inputs and outputs as parameters, so tests can call it; `os.Exit` in tested code would end the test binary.
- **A struct with a slice field can't be compared with `==`.** Adding `percentiles []float64` to `configFlags` broke a test that compared two configs; compare the fields, using `slices.Equal` for the slice.
- **Growing a slice at the front needs `copy`, not `append`.** `append` only grows at the back; the sketch's bucket store allocates a bigger slice and copies the old counts to its end. `slices.Compact` and `slices.Sort` work in place, so sort a `slices.Clone` when the caller's order matters.
- **Measure before assuming.** Building a sketch for every group looked free with 6 groups, but with 10,000 it raised memory from 12 MB to 67 MB even without `-p`. Making it a `nil` pointer unless needed fixed it.
- **Tests belong to the package, not to a file.** Any number of `_test.go` files can test unexported functions, and changing a test row first, then the code, makes each behaviour change visible.

## Limitations and future ideas

- **Many groups with `-p`:** each group's sketch is about 5.5 KB, so a million groups would need about 5.5 GB. A bounded-size sketch that merges its lowest buckets (as DDSketch allows) would cap this, at the cost of accuracy for the lowest percentiles.
- **Integer fields:** percentiles are estimates, so p50 of whole numbers can print as e.g. `2.98`. Rounding when every value in a group is a whole number would fix this.
- **`-v` (verbose):** an idea not built: optionally log every skipped line as well as the summary, e.g. to `grep` through them.
- **Nested field paths** (`meta.host`) are not supported.
- Standard library only.

---

Built as Project 1 of a personal Go learning roadmap. Completed November 2026.
