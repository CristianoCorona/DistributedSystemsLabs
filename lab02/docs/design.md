# Design notes — basic part

This file explains *why* the code is the way it is. The code comments
explain *what* each piece does; this file is not repeated there.

## Task life cycle

Every map and reduce task is in one of three states:

```
idle ──assigned──▶ inProgress ──report──▶ completed
  ▲                    │
  └──── older than 10 s (re-assigned) ────┘
```

Reduce tasks are only assigned once every map task is `completed`,
because each reduce task reads the output of all map tasks.

## RPC interface

| RPC | Sent by the worker when | Reply |
|---|---|---|
| `Coordinator.RequestTask` | it is ready for work | `MapTask`, `ReduceTask`, `WaitTask` or `ExitTask`, plus the fields that task needs |
| `Coordinator.ReportTask` | all output files of its task have been renamed to their final names | empty |

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Number of RPCs | two: request and report | easier to read; a report does not depend on asking for more work |
| No task available | reply `WaitTask`; the worker sleeps and asks again | blocking the handler with `sync.Cond` would need a timer anyway (a condition variable is not woken by a timeout) |
| Detecting late tasks | lazily, inside `RequestTask` | a late task can only be re-assigned to a worker that asks; a background goroutine would add concurrency without benefit |
| Shared state | one `sync.Mutex` for the whole coordinator, `Done` included | all operations are short; no waiting while holding the lock |
| Zero value of `TaskType` | `WaitTask` | net/rpc replies start zeroed: an unset reply must mean "wait", not "run map task 0" |
| Duplicate or late reports | the first report completes the task, whatever its attempt number; later ones are ignored | map and reduce are deterministic and output is renamed atomically, so every attempt produces the same bytes |
| Attempt number | sent and reported, unused in decisions | costs nothing now; needed in the advanced parts to know which attempt's output to use |
| Progress counters | `mapsLeft`, `reducesLeft` | O(1) `Done` and phase checks; invariant: decremented exactly once per task, guarded by the `completed` check |
| Testability | `assignTask(now, ...)` takes the time as a parameter; `newCoordinator` builds the state without starting the RPC server | unit tests can simulate 10 s passing without sleeping, and create many coordinators in one process |

## Worker: map side

| Decision | Choice | Why |
|---|---|---|
| Intermediate format | one JSON object per `KeyValue`, `mr-X-Y` per map task X and reduce task Y | suggested by the lab; readable when debugging |
| Empty buckets | always create all `NReduce` files | a reduce task can treat a missing file as an error, not as "no keys" |
| Atomic writes | temporary file in the same directory, then `os.Rename` | a rename within one directory is atomic and replaces the old file: readers see the old version or the new one, never a partial one |
| Temporary file names | `tmp-<final name>-<random>` | never match `mr-X-Y` or `mr-out*`; `test-mr.sh` reads every `mr-out*` file, so a leftover named `mr-out-0.tmp...` from a crashed worker would corrupt its check |
| `fsync` before rename | not done | the failures we handle are process crashes, and the OS keeps written data after a process dies; `fsync` protects against machine crashes (relevant for the coordinator's state in 2.2) |
| Output directory | a `dir` parameter (`"."` in production) | unit tests write into a temporary directory; in 2.1 the worker may keep its files in its own directory |
| Reporting | `doMap` never reports; the caller reports only if it returned `nil` | a failed task is simply not reported, and the coordinator re-assigns it after the timeout |

## Assumptions the worker must respect

- A worker that receives `WaitTask` keeps asking: it does not exit or block.
  Lazy timeout detection depends on it.
- A worker reports a task only after the last output file is renamed.

## What changes in the advanced parts

- **2.1** — a `completed` map task can go back to `idle` when the worker
  holding its output dies; the coordinator must record where each map
  output lives.
- **2.2** — the coordinator state is persisted; `TaskType` and task
  states are `iota` constants, so their order must not change once
  persisted as numbers.
- **2.3** — the fixed 10 s timeout becomes a decision based on progress
  reported by the workers.