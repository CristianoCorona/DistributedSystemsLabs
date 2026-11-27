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
 
