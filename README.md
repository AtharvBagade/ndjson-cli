# ndstats

A Go command-line tool that streams newline-delimited JSON (NDJSON), groups records by a field, and reports statistics of a numeric field for each group, in constant memory.

```sh
cat access.ndjson | ndstats -group region -field latency_ms
```

## Goal

Log files and event exports are often NDJSON and often too large to load into memory. `ndstats` answers questions like *"what's the latency per region?"* or *"how many bytes per status code?"* by reading the input once, line by line, and keeping only a small running summary per group. Memory depends on the number of groups, not the size of the file.

The project is also a deliberate exercise in idiomatic Go: standard library only, streaming I/O, errors as values, and small functions that can be tested.

## Status

| Area | State |
|---|---|
| Stream NDJSON from stdin | Done |
| `-group`: group by a string or numeric field | Done |
| `-field`: count, min, max, mean per group | Done |
| Bad lines skipped and reported with line numbers | Done |
| `-strict`: stop at the first bad line instead | Done |
| Whitespace trimmed from values and group keys; blank lines ignored | Done |
| Long lines (up to 1 MB) | Done |
| Deterministic, sorted output | Done |
| Code split into testable functions taking `io.Reader` / `io.Writer` | Done |
| One summary of skipped lines per reason, with the first line number | Done |
| Table-driven tests, `go test -race` (117 cases, ~90% coverage) | Done |
| File arguments (`ndstats ... a.ndjson b.ndjson`) | Next |
| `-p 50,95,99` percentiles | Planned |
| Verified constant memory on a multi-GB file | Planned |
| `-v`: log every skipped line as well as the summary | Planned |

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
| `-strict` | `false` | Stop at the first line that can't be assigned to a group, instead of skipping it |

Input is read from stdin. If a required flag is missing, `ndstats` names it, prints usage, and exits with status 2:

```
$ ndstats -field latency_ms
missing required flag(s): -group
```

### Examples

Latency per region:

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
2026/11/14 15:05:02 line 9: invalid JSON: unexpected end of JSON input
$ echo $?
1
```

Columns:

- **count**: records in the group.
- **values**: records with a usable `-field` value.
- **skipped**: records in the group whose `-field` value was missing or unusable.
- **min**, **max**, **mean**: computed over the usable values. They print as `-` when a group has none.

The table goes to stdout and diagnostics go to stderr, so the output can be piped or redirected cleanly.

## Input format

- Each line must be a JSON **object**. Lines can be up to 1 MB long.
- **Blank lines** (empty or whitespace only) are ignored. They still count toward line numbers, so diagnostics match the file.
- **Bad lines**: a line that isn't valid JSON, isn't an object, or has no usable group key is skipped and counted in "Lines skipped". At the end, a summary on stderr gives the count for each reason, with the line number and error of its first occurrence. With `-strict`, the first such line stops the run with exit status 1.
- Fields are addressed by top-level name. Nested fields (e.g. `meta.host`) are not supported yet.
- **Group key** (`-group`): strings are used with surrounding whitespace trimmed, so `" us-east "` and `"us-east"` are one group. Numbers are formatted without trailing zeros, so `200` and `"200"` fall into the same group. A group field that is missing, `null`, empty or whitespace only, a boolean, an object or an array makes the line a bad line.
- **Value** (`-field`): JSON numbers and numeric strings are accepted, with surrounding whitespace trimmed (`"45"`, `" 7.5"`). Missing, `null`, non-numeric strings (`"fast"`), `"NaN"`, `"Inf"` and other types are not used as values. This isn't a bad line, even with `-strict`: the record still counts toward its group, and the value is counted in that group's **skipped** column.

## How it works

```
io.Reader → bufio.Scanner → json.Unmarshal → per-group stats → sorted table
 (stdin)     (1 MB cap)       (one line)      (running totals)   (io.Writer)
```

- **`aggregate(r io.Reader, cfg)`** owns the scanner, the line limit, line counting and read errors. Because it takes an `io.Reader`, stdin, files and `strings.NewReader` in tests all go through the same code. It also decides what happens to a bad line: skip and count it, or, with `-strict`, return it as an error.
- **`processLine`** parses one line, resolves its group key and updates that group. It returns an error only when the line can't be assigned to a group, and it doesn't know about strict mode. Each kind of problem has a sentinel error (`errBlankLine`, `errBadJSON`, `errNoGroupKey`, `errBadGroupKey`), wrapped with details, so `aggregate` can tell them apart with `errors.Is`.
- **`groupStat`** keeps `count`, `valueCount`, `skippedCount`, `sum`, `min` and `max`. Each line updates these in place and is then discarded.
- **`totals`** counts lines and, for each skip reason, a `skipReason` with the count, first line number and first error. Only the first occurrence is kept, so this stays constant-size too.
- **`printFinalStats(w io.Writer, …)`** sorts the groups and writes the table to stdout. **`printSkipSummary(w io.Writer, …)`** writes the skip summary to stderr, labelling each reason with its sentinel's own text (`kind.Error()`).

**Why memory stays constant:** no record or value is kept after its line is processed. The scanner's buffer is capped at 1 MB (`maxLineSize = 1 << 20`), and each group holds six numbers. Memory grows with the number of distinct groups, not with the input size.

## Design decisions

| Decision | Choice | Why |
|---|---|---|
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
| Mean | Derived from `sum / valueCount` at print time | It can't go stale, and it costs one division per group instead of one per line. |
| min/max start | Set by the first value | The zero value `0` would be wrong for all-positive data. |
| Output order | Highest count first, ties by key | Go randomises map iteration on purpose, so an explicit, total order keeps output deterministic and testable. |
| Line limit | 1 MB | Large enough for real log lines, small enough that one bad input can't exhaust memory. |
| Required flags | Both `-group` and `-field` | Keeps the output format fixed for now. |
| Errors | Wrapped with `%w`, checked with `errors.Is` | `main` can recognise `bufio.ErrTooLong` under added context and explain the limit. |

## Testing

```sh
go vet ./...                 # clean
go test ./...                # 117 table-driven cases
go test -race ./...
go test -cover ./cmd/ndstats # ~90% of statements
```

The tests are table-driven and split by topic:

- **`helpers_test.go`** covers the value-in, value-out functions: `toFloat64` (numbers, numeric and padded strings, `"NaN"`, `"Inf"`, `"1e400"`, wrong types), `toString`, `groupKeyOf`, the `groupStat` methods (including all-negative data and zero as a real value), `handleGroupStat`, `kindOf` and `recordSkip` (only the first error per reason is kept), the sort order (repeated runs catch random map order), and `parseConfig` (with a fresh `flag.FlagSet` per case).
- **`aggregate_test.go`** covers `processLine` (each kind of good and bad line, checked with `errors.Is` against the sentinels), error wrapping (`errors.As` finds the original `*json.SyntaxError`), `aggregate` in default and strict mode, an end-to-end run on `testdata/small.ndjson` against a hand-computed table, the 69 KB line in `testdata/longline.ndjson`, a line over the 1 MB limit (`errors.Is(err, bufio.ErrTooLong)`), and the exact table and skip-summary output written to a `bytes.Buffer`.

`testdata/small.ndjson` has one edge case per line: malformed and blank lines, numbers vs strings, missing, `null`, `"fast"`, `"NaN"`, `"Inf"`, booleans, nested objects and padded numeric strings. `testdata/longline.ndjson` contains a realistic checkout log line with a full order response body, longer than `bufio.Scanner`'s 64 KB default.

`main` itself isn't tested. It only connects `parseConfig`, `aggregate`, `printFinalStats` and `printSkipSummary`, which are.

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
- **Accept interfaces (`io.Reader`, `io.Writer`) at the boundaries** so the same code works with stdin, files and tests.
- **Tests belong to the package, not to a file.** Any number of `_test.go` files can test unexported functions, and changing a test row first, then the code, makes each behaviour change visible.

## Limitations and next steps

- **File arguments:** read one or more files, falling back to stdin, with `file:line` in diagnostics.
- **Percentiles (`-p`):** choose a constant-memory method (histogram, reservoir sample or streaming estimator), since keeping every value would break the memory guarantee.
- **Constant memory** still has to be measured on a multi-GB file.
- **`-v` (verbose):** optionally log every skipped line as well as the summary, e.g. to `grep` through them.
- **Nested field paths** (`meta.host`) are not supported.
- Standard library only.

---

Built as Project 1 of a personal Go learning roadmap.
