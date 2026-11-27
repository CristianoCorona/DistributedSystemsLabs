# Testing

What each test does is written in its own comment; this file explains
the strategy, which properties are covered, and how we know the tests
are meaningful.

## Two layers

| Layer | Where | What it covers | Speed |
|---|---|---|---|
| Unit tests | `src/mr/coordinator_test.go` | the coordinator's scheduling logic, with a fake clock and no RPC | ~1 s |
| Unit tests | `src/mr/worker_test.go` | the worker's file handling, in temporary directories | < 1 s |
| End-to-end tests | `src/main/test-mr.sh` (from the lab) | real processes, plugins and files: output correctness, parallelism, crashes | minutes |

Unit tests come first because they pinpoint the faulty rule (e.g. "map
task 3 was assigned twice") while `test-mr.sh` only reports that the
final output is wrong.

```bash
cd src && go test -race -v ./mr/
cd src/main && bash test-mr.sh
```

## Properties covered by the unit tests

| Property | Tests |
|---|---|
| Reduce tasks start only after every map task completed | `TestMapsAreAssignedFirst`, `TestFullJobSequence` |
| A task in progress for more than 10 s is re-assigned (boundary included) | `TestTimeoutReassignsTask` |
| A re-assigned task gets a fresh deadline | `TestReassignedTaskRestartsTimer` |
| Each task is counted once, whatever the number of reports | `TestReportCountsEachTaskOnce` |
| Malformed reports are rejected without changing the state | `TestReportRejectsInvalidArgs` |
| The job is done only when every task completed | `TestFullJobSequence`, `TestDoneWithNoReduceTasks` |
| Without failures each task is assigned exactly once, under concurrency | `TestConcurrentWorkersRunEachTaskOnce` |
| An unset reply means "wait" | `TestZeroReplyMeansWait` |
| Map output is split by `ihash`, with no pair lost | `TestDoMapPartitionsByKey` |
| Every bucket gets a file, even an empty one | `TestDoMapCreatesEmptyFiles` |
| An unreadable input is an error and produces no file | `TestDoMapMissingInput` |
| A file being written is invisible under its final name | `TestWriteAtomicallyIsInvisibleUntilDone` |
| A failed write leaves no file and does not damage an older one | `TestWriteAtomicallyFailureLeavesNothing`, `TestWriteAtomicallyFailureKeepsOldFile` |

## Mutation check

To check that the tests distinguish correct from incorrect code, we
introduced one bug at a time in `coordinator.go` and ran the unit tests.
Every mutant made at least one test fail.

Last run: 2026-11-27, on the first version of the coordinator.

| Mutant | Caught by |
|---|---|
| `>=` instead of `>` in the timeout check | `TestTimeoutReassignsTask` |
| late tasks never re-assigned | `TestTimeoutReassignsTask`, `TestReassignedTaskRestartsTimer` |
| no `completed` check in `ReportTask` (double counting) | `TestReportCountsEachTaskOnce` |
| `Done` ignores `mapsLeft` | `TestDoneWithNoReduceTasks` |
| reduce tasks handed out during the map phase | `TestMapsAreAssignedFirst`, `TestTimeoutReassignsTask`, `TestReassignedTaskRestartsTimer` |
| `attempt` not incremented | `TestMapsAreAssignedFirst`, `TestTimeoutReassignsTask` |
| `started` not updated on re-assignment | `TestReassignedTaskRestartsTimer` |
| `NReduce` not sent to map workers | `TestMapsAreAssignedFirst` |
| no task ID range check | `TestReportRejectsInvalidArgs` |

`TestReassignedTaskRestartsTimer` was added because of this check:
without it, the "`started` not updated" mutant survived.

Same check on the map side of `worker.go` (2026-11-27):

| Mutant | Caught by |
|---|---|
| every key sent to bucket 0 | `TestDoMapPartitionsByKey` |
| empty buckets get no file | `TestDoMapCreatesEmptyFiles` |
| only the first pair of a bucket encoded | `TestDoMapPartitionsByKey` |
| temporary file not removed on failure | `TestWriteAtomicallyFailureLeavesNothing` |
| output written in place, without a temporary file | `TestWriteAtomicallyIsInvisibleUntilDone`, `TestWriteAtomicallyFailureKeepsOldFile` |
| rename before writing the data | every worker test except `TestDoMapMissingInput` |

`TestDoMapCreatesEmptyFiles` and the two "atomically" tests that check
what a reader sees were added because of this check: the first version
of the tests let the "empty buckets" and "written in place" mutants survive.

## End-to-end tests (`test-mr.sh`)

To be completed when the worker is implemented and the script is run.