# ndstats

A command-line tool, written in Go, that groups newline-delimited JSON (NDJSON) records by a field and reports counts and percentiles of a numeric field for each group.

> **Status: not implemented yet.** The repository has no code so far. Each section below will be filled in once the matching code exists.

## Usage

_To be written once `cmd/ndstats` builds._

## Input format

_To be written: what counts as a valid line, how fields are addressed, and how missing or non-numeric values are handled._

## How it works

_To be written: the streaming pipeline (reader → scanner → parser → aggregator → printer) and why memory stays constant._

## Percentile method

_Not chosen yet._

## Design decisions

_Not made yet: malformed-line handling, map vs struct parsing._

## Testing

```sh
go test ./...
go test -race ./...
go vet ./...
```

_No tests yet._

## What I learned

_See [BUGS.md](BUGS.md) once it has entries._

## Limitations and next steps

- Standard library only for v1.
- Must stream multi-GB input in constant memory.

---

Built as Project 1 of a personal Go learning roadmap.
