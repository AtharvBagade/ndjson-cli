# ndstats — project context for Claude Code

## What this project is

ndstats is a command-line tool written in Go. It reads newline-delimited JSON (NDJSON) from stdin or from files, groups records by a chosen field, and prints counts and percentiles of a numeric field for each group.

Example:

```sh
cat logs.ndjson | ndstats -group status -field latency_ms -p 50,95,99
ndstats -group region -field bytes access-1.ndjson access-2.ndjson
```

This is Project 1 of a personal Go learning roadmap (Linear: ATH-61, project "Go Roadmap"). The goal is to learn Go fundamentals, not just to ship a tool.

## Who is working on it

- A backend engineer with a strong JavaScript/TypeScript and Node.js background who is new to Go.
- Has finished the basics and the methods-and-interfaces sections of A Tour of Go. Concurrency comes later in the roadmap.
- Learns best by hitting a real problem and then understanding the mechanism behind it.

## How Claude should help

- Do not write the implementation unless explicitly asked. Explain the concept, point at the relevant standard-library API or Go doc, and let the user write the code.
- When reviewing code, name the problem and explain why it happens; suggest the fix in words or with a minimal snippet, not a full rewrite.
- Explain how things work underneath (memory layout, what the runtime does, why the compiler complains), not just what to type.
- Where it helps, compare with the JavaScript/Node equivalent (e.g. `io.Reader` vs Node streams, errors as values vs exceptions, zero values vs `undefined`).
- Prefer links to go.dev, pkg.go.dev, and the Go blog over third-party sources.
- When the user hits a bug, remind them to log it in `BUGS.md`.

## Constraints

- Standard library only for the first version.
- Must stream input in constant memory: never read a whole file into memory, and never store every value of a multi-GB file.
- Malformed JSON lines must not crash the tool; how they are handled (skip and count, or fail with a line number) is a design decision to record in the README.

## Definition of done

- Streams a multi-GB file in constant memory.
- Table-driven tests pass.
- `go vet ./...` is clean.
- `go test -race ./...` passes.
- `BUGS.md` lists each bug hit, its symptom, what the compiler or runtime said, and the fix.

## Commands

```sh
go run ./cmd/ndstats -group <field> -field <field> [files...]
go test ./...
go test -race ./...
go vet ./...
```

## Go concepts this project should exercise

`bufio.Scanner` (and its 64 KB default line limit), `io.Reader`, `encoding/json`, `map[string]any` vs structs, type assertions, the `flag` package, maps and slices (nil-map writes, slice aliasing), `sort`, error wrapping with `%w`, `errors.Is` / `errors.As`, `defer`, and table-driven tests.

## Writing the README

When asked to draft or update `README.md`, base it on the code as it currently exists. Do not describe features that are not implemented. Use this structure:

1. One-line description: what the tool does.
2. Usage: install/build command, every flag with its default, and two or three real examples with sample output.
3. Input format: what counts as a valid line, how nested fields are addressed (if supported), and how non-numeric or missing values are handled.
4. How it works: the streaming pipeline (reader → scanner → parser → aggregator → printer) and why memory stays constant.
5. Percentile method: which approach was chosen (exact, reservoir sample, histogram, or streaming estimator), its accuracy trade-off, and why.
6. Design decisions: malformed-line handling, map vs struct parsing, and any other choice with a real alternative, each with a short reason.
7. Testing: how to run tests and what the tests cover.
8. What I learned: Go-specific lessons, with links to entries in `BUGS.md`.
9. Limitations and next steps.

Keep the README factual and short. Before writing it, ask the user for anything that cannot be found in the code: for example, the reason behind a design decision.
