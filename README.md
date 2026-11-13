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
| Stream NDJSON from stdin | ✅ Done |
| `-group`: group by a string or numeric field | ✅ Done |
| `-field`: count, min, max, mean per group | ✅ Done |
| Malformed lines skipped and reported with line numbers | ✅ Done |
| Long lines (up to 1 MB) | ✅ Done |
| Deterministic, sorted output | ✅ Done |
| Code split into testable functions taking `io.Reader` / `io.Writer` | ✅ Done |
| Table-driven tests, `go test -race` | 🔜 Next |
| File arguments (`ndstats ... a.ndjson b.ndjson`) | 🔜 Planned |
| `-p 50,95,99` percentiles | 🔜 Planned |
| Verified constant memory on a multi-GB file | 🔜 Planned |

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

Input is read from stdin. If a required flag is missing, `ndstats` names it, prints usage, and exits with status 2:

```
$ ndstats -field latency_ms
missing required flag(s): -group
```

### Examples

Latency per region:

```
$ ndstats -group region -field latency_ms < testdata/small.ndjson
2026/11/13 21:23:04 line 9: invalid JSON: unexpected end of JSON input
2026/11/13 21:23:04 line 11: invalid JSON: unexpected end of JSON input
region         count  values  skipped        min        max       mean
us-east            7       5        2       3.20     980.00     251.06
eu-west            6       4        2      18.90      45.00      29.00
ap-south           4       1        3    1200.50    1200.50    1200.50
Total lines processed: 19
Lines skipped: 2
```

Bytes per status code, with diagnostics hidden:

```
$ ndstats -group status -field bytes < testdata/small.ndjson 2>/dev/null
status         count  values  skipped        min        max       mean
200               11      10        1      64.00    4096.00     916.80
404                3       3        0     128.00     128.00     128.00
500                2       2        0      64.00      90.00      77.00
503                1       1        0       0.00       0.00       0.00
Total lines processed: 19
Lines skipped: 2
```

Columns:

- **count**: records in the group.
- **values**: records with a usable `-field` value.
- **skipped**: records in the group whose `-field` value was missing or unusable.
- **min**, **max**, **mean**: computed over the usable values. They print as `-` when a group has none.

The table goes to stdout and diagnostics go to stderr, so the output can be piped or redirected cleanly.

## Input format

- Each line must be a JSON **object**. A line that isn't valid JSON, or isn't an object, is skipped and reported with its line number. Lines can be up to 1 MB long.
- Fields are addressed by top-level name. Nested fields (e.g. `meta.host`) are not supported yet.
- **Group key** (`-group`): strings are used as-is. Numbers are formatted without trailing zeros, so `200` and `"200"` fall into the same group. A record whose group field is missing, `null`, a boolean, an object or an array is skipped and reported.
- **Value** (`-field`): JSON numbers and numeric strings (`"45"`) are accepted. Missing, `null`, non-numeric strings (`"fast"`), `"NaN"`, `"Inf"` and other types are not used as values. The record still counts toward its group, and the value is counted in that group's **skipped** column.

## How it works

```
io.Reader → bufio.Scanner → json.Unmarshal → per-group stats → sorted table
 (stdin)     (1 MB cap)       (one line)      (running totals)   (io.Writer)
```

- **`aggregate(r io.Reader, cfg)`** owns the scanner, the line limit, line counting and read errors. Because it takes an `io.Reader`, stdin, files and `strings.NewReader` in tests all go through the same code.
- **`processLine`** parses one line, resolves its group key and updates that group. It returns an error only when the line can't be assigned to a group.
- **`groupStat`** keeps `count`, `valueCount`, `skippedCount`, `sum`, `min` and `max`. Each line updates these in place and is then discarded.
- **`printFinalStats(w io.Writer, …)`** sorts the groups and writes the table.

**Why memory stays constant:** no record or value is kept after its line is processed. The scanner's buffer is capped at 1 MB (`maxLineSize = 1 << 20`), and each group holds six numbers. Memory grows with the number of distinct groups, not with the input size.

## Design decisions

| Decision | Choice | Why |
|---|---|---|
| Malformed lines | Skip, count, log with line number | One bad line in a multi-GB log shouldn't abort the whole run. The final count makes skipped lines visible. |
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
go vet ./...      # clean
go test ./...
go test -race ./...
```

Tests haven't been written yet. The code is split so that each piece can be tested on its own: `toFloat64`, `groupKeyOf` and `processLine` take values and return values, `aggregate` takes any `io.Reader`, and `printFinalStats` writes to any `io.Writer`. `testdata/small.ndjson` covers each edge case above: malformed and blank lines, numbers vs strings, missing, `null`, `"fast"`, `"NaN"`, `"Inf"`, booleans and nested objects. `testdata/longline.ndjson` has a line over 64 KB.

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
- **Errors are values.** Return them, wrap them with `%w`, and let `main` decide what to do. `errors.Is` finds a sentinel error through the wrapping.
- **Accept interfaces (`io.Reader`, `io.Writer`) at the boundaries** so the same code works with stdin, files and tests.

## Limitations and next steps

- **Tests:** table-driven tests for each function and an end-to-end test on `testdata/small.ndjson`; `go test -race`.
- **File arguments:** read one or more files, falling back to stdin, with `file:line` in diagnostics.
- **Percentiles (`-p`):** choose a constant-memory method (histogram, reservoir sample or streaming estimator), since keeping every value would break the memory guarantee.
- **Decided, not yet implemented:**
  - trim whitespace around numeric strings (`" 7.5"` is currently skipped);
  - skip blank lines quietly (they're currently reported as invalid JSON);
  - replace the per-line log with one summary per skip reason.
- **Constant memory** still has to be measured on a multi-GB file.
- **Nested field paths** (`meta.host`) are not supported.
- Standard library only.

---

Built as Project 1 of a personal Go learning roadmap.
