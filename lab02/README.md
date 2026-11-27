# Lab 2 — MapReduce

A MapReduce system in Go: one coordinator hands out map and reduce tasks
over RPC, and one or more workers run them, re-assigning tasks whose
worker does not finish within 10 seconds.

Based on the MIT 6.5840 (2023) lab skeleton. Our code lives in
`src/mr/` (`coordinator.go`, `worker.go`, `rpc.go`); the files in
`src/main/` are unchanged.

Assignment text: [`docs/assignment.md`](docs/assignment.md).
Design decisions and their rationale: [`docs/design.md`](docs/design.md).

## Build and run

All commands run from `src/main`.

```bash
go build -race -buildmode=plugin ../mrapps/wc.go   # rebuild after any change in src/mr
rm -f mr-out*
go run -race mrcoordinator.go pg-*.txt             # terminal 1
go run -race mrworker.go wc.so                     # terminals 2, 3, ...
cat mr-out-* | sort | more
```

The plugin and the program must be built with the same flags
(both with `-race` or both without).

## Tests

From `lab02/`:

```bash
(cd src && go test -race ./mr/)          # unit tests (coordinator and worker)
(cd src/main && bash test-mr.sh)         # end-to-end tests from the lab
```

Test strategy, covered properties and mutation check:
[`docs/testing.md`](docs/testing.md).

## Requirements

| Requirement | Status | Verified by |
|---|---|---|
| Map tasks before reduce tasks; reduce tasks wait for all maps | done | unit tests |
| Re-assign a task not completed within 10 s | done | unit tests, `test-mr.sh` crash test |
| Each task runs once when no worker fails | done | unit tests, `test-mr.sh` job count test |
| Map and reduce tasks run in parallel | done | `test-mr.sh` parallelism tests |
| Worker: map tasks, `mr-X-Y` intermediate files | done | unit tests |
| Worker: reduce tasks, `mr-out-X` output in `%v %v` format | done | unit tests, `test-mr.sh` wc and indexer tests |
| Atomic output (temp file + rename) | done | unit tests |
| Workers exit when the job is done | done (`ExitTask`, or coordinator unreachable) | unit tests (coordinator side), `test-mr.sh` early exit test |
| `test-mr.sh` passes | done (one full run, 2026-11-27) | see `docs/testing.md` |

## Known limitations

- A task whose worker died is re-assigned only when another worker asks
  for work (see `docs/design.md`); if every worker is dead, the job stops.