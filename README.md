# ndstats

A Go command-line tool that streams newline-delimited JSON (NDJSON), groups records by a field, and reports statistics and percentiles of a numeric field for each group, in constant memory.

```sh
ndstats -group region -field latency_ms -p 50,95,99 access-1.ndjson access-2.ndjson
cat access.ndjson | ndstats -group region -field latency_ms
```

## Why

Log files and event exports are often NDJSON and often too large to load into memory. `ndstats` answers questions like *"what's the p99 latency per region?"* or *"how big are responses for each status code?"* by reading the input once, line by line, and keeping only a small running summary per group. Memory depends on the number of groups, not the size of the file.

## Highlights

- **Constant memory:** about 12 MB of peak memory whether the input is 50 MB or 1 GB ([measured](#performance)).
- **Streaming percentiles:** p50/p95/p99/p99.9 per group, guaranteed within 1% of the true value, using a DDSketch-style log histogram implemented from scratch.
- **Robust to messy input:** bad lines are skipped and summarised by reason with the first `file:line`; `-strict` fails fast instead.
- **Unix-style CLI:** reads files or stdin, merges multiple files, meaningful exit codes, results on stdout and diagnostics on stderr.
- **Well tested:** 175 table-driven test cases, about 95% coverage, race-detector clean, with percentile accuracy checked against exact results.
- **Standard library only.**

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

Messy input, read from stdin. Bad lines are counted, and the summary on stderr gives one line per reason, however many bad lines there are:

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
- Fields are addressed by top-level name; nested fields (e.g. `meta.host`) aren't supported.
- **Group key** (`-group`): strings are used with surrounding whitespace trimmed, so `" us-east "` and `"us-east"` are one group. Numbers are formatted without trailing zeros, so `200` and `"200"` fall into the same group. A group field that is missing, `null`, empty or whitespace only, a boolean, an object or an array makes the line a bad line.
- **Value** (`-field`): JSON numbers and numeric strings are accepted, with surrounding whitespace trimmed (`"45"`, `" 7.5"`). Missing, `null`, non-numeric strings (`"fast"`), `"NaN"`, `"Inf"` and other types are not used as values. This isn't a bad line, even with `-strict`: the record still counts toward its group, and the value is counted in that group's **skipped** column.

## Percentile method

**Method:** a log-bucketed histogram, as in [DDSketch](https://arxiv.org/abs/1908.10693) (Masson, Rim & Lee, VLDB 2019), in [`cmd/ndstats/sketch.go`](cmd/ndstats/sketch.go).

**Why not exact:** an exact percentile needs every value kept and sorted. For a 1 GB log that's about 6 million `float64`s (48 MB), growing with the input, which breaks the constant-memory guarantee.

**How it works:** each group counts its values in buckets whose bounds grow by a fixed ratio γ = (1 + α) / (1 − α), with α = 0.01. Bucket *i* holds the values in (γ^(i−1), γ^i]; a value's bucket is `ceil(log(v) / log γ)`. To find a percentile, walk the buckets in order, adding up their counts, until the running count reaches the percentile's rank, and report that bucket's midpoint 2γ^i / (γ + 1). Negative values are counted by magnitude in a second set of buckets, and values with |v| < 10⁻⁹ (including zero) in a zero bucket.

**Accuracy:** every reported percentile is within **±1% of the true value**, relative to its size: p99 = 162.41 means the true p99 is between about 160.8 and 164.0. The bound holds at every percentile, including the tail (p99, p99.9), and at any scale: 0.5 ms and 4 GB get the same guarantee. The tests check this against exact percentiles on log-normal, uniform, zero-heavy, negative, integer and 18-orders-of-magnitude data.

**Definition:** nearest rank, so pN is the smallest value that at least N% of values are ≤. For p99 of 5 values that's the largest value. The other common definition rounds down and would report the 4th, understating the tail. `p0` and `p100` return the exact min and max, and every estimate is kept within them.

**Memory:** a sketch only stores counts for the buckets between its smallest and largest value: a few hundred for typical data (0.2 ms to 2.4 s is under 500 buckets), about 5.5 KB per group. It doesn't grow with the number of values, and it's only created when `-p` is given.

## Performance

Peak memory measured with `/usr/bin/time -l` (macOS, Apple Silicon) on generated access logs, grouped into 6 regions:

| Input | Lines | Peak memory | Time |
|---|---|---|---|
| 50 MB | 297,674 | 11.2 MB | 0.40 s |
| 500 MB | 2,976,419 | 11.5 MB | 3.80 s |
| 1 GB | 6,095,675 | 11.9 MB | 7.94 s |

20× the input adds under 1 MB of memory, and time grows linearly (about 130 MB/s, bound by JSON parsing). Adding `-p 50,95,99,99.9` doesn't change either measurably. Memory grows only with the number of groups: about 5.5 KB per group with `-p`.

To reproduce:

```sh
go build -o /tmp/ndstats ./cmd/ndstats
go run ./testdata/gen -size 1G > /tmp/1g.ndjson
/usr/bin/time -l /tmp/ndstats -group region -field latency_ms /tmp/1g.ndjson > /dev/null
```

## Testing

```sh
go vet ./...                 # clean
go test ./...                # 175 table-driven cases
go test -race ./...
go test -cover ./cmd/ndstats # ~95% of statements
```

The tests are table-driven and split by topic:

- **`sketch_test.go`:** percentile estimates against exact results at 11 quantiles from p0 to p100 (including p99.9) on 11 data shapes (log-normal, uniform, zero-heavy, negative, integers, 18 orders of magnitude, single and equal values), all within 1%.
- **`helpers_test.go`:** value conversion (`"NaN"`, `"Inf"`, padded strings, wrong types), group keys, `-p` and flag parsing, the per-group statistics, and deterministic sort order.
- **`aggregate_test.go`:** each kind of good and bad line with `errors.Is`/`errors.As` on the sentinel errors, default and strict mode, an end-to-end run on `testdata/small.ndjson` against hand-computed results, lines over 64 KB and over the 1 MB limit, and the exact table output.
- **`run_test.go`:** the CLI as a whole with real files in `t.TempDir()`: merging, missing files, directories, `-` for stdin, per-file line numbers and `-strict`.

`testdata/small.ndjson` has one edge case per line, and `testdata/longline.ndjson` has a 69 KB line, longer than `bufio.Scanner`'s 64 KB default.

**`testdata/gen`** generates large, reproducible synthetic access logs (log-normal latencies, a configurable fraction of bad lines) for the measurements above. Run it with `go run ./testdata/gen`:

| Flag | Default | Meaning |
|---|---|---|
| `-lines` | 0 | Number of lines to write |
| `-size` | | Stop after about this many bytes (`500M`, `2G`), instead of `-lines` |
| `-groups` | 0 | Use `region-0` … `region-N` instead of six real regions, to test many groups |
| `-bad` | 0.001 | Fraction of bad lines (truncated JSON, missing `region`, `"fast"` latency, blank) |
| `-seed` | 1 | Random seed |

`main` itself isn't tested. It only parses flags, calls `run` and picks the exit code.

## Limitations and future ideas

- **Many groups with `-p`:** each group's sketch is about 5.5 KB, so a million groups would need about 5.5 GB. A bounded-size sketch that merges its lowest buckets (as DDSketch allows) would cap this, at the cost of accuracy for the lowest percentiles.
- **Integer fields:** percentiles are estimates, so p50 of whole numbers can print as e.g. `2.98`. Rounding when every value in a group is a whole number would fix this.
- **`-v` (verbose):** an idea not built: optionally log every skipped line as well as the summary, e.g. to `grep` through them.
- **Nested field paths** (`meta.host`) aren't supported.

## License

[MIT](LICENSE)
