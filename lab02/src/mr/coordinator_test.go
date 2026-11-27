package mr

//
// Unit tests for the coordinator's scheduling logic.
//
// They never start the RPC server and never sleep: they build the state
// with newCoordinator, call assignTask with an explicit (fake) time, and
// call ReportTask directly as a plain method.
//
// Run with:  go test -race ./mr/
//

import (
	"sync"
	"testing"
	"time"
)

// t0 is an arbitrary fixed "now" used as the starting time of every test.
var t0 = time.Unix(1_000_000, 0)

// request asks c for a task at time now, exactly like a worker would,
// but without RPC. It returns the reply by value for easy checking.
func request(c *Coordinator, now time.Time) RequestTaskReply {
	reply := RequestTaskReply{} // zero-allocated, as net/rpc requires
	c.assignTask(now, &reply)
	return reply
}

// report tells c that a task finished, and fails the test if the
// coordinator returns an error.
func report(t *testing.T, c *Coordinator, typ TaskType, id, attempt int) {
	t.Helper() // on failure, report the caller's line, not this one
	args := ReportTaskArgs{Type: typ, TaskID: id, Attempt: attempt}
	if err := c.ReportTask(&args, &ReportTaskReply{}); err != nil {
		t.Fatalf("ReportTask(%+v) returned error: %v", args, err)
	}
}

// TestZeroReplyMeansWait documents a design choice: a reply whose Type
// is never set must make the worker wait, not run map task 0.
func TestZeroReplyMeansWait(t *testing.T) {
	if (RequestTaskReply{}).Type != WaitTask {
		t.Fatal("the zero value of TaskType must be WaitTask")
	}
}

// TestMapsAreAssignedFirst checks that map tasks are handed out in order,
// with the fields a map worker needs, and that once all maps are running
// the next worker is told to wait (reduce tasks must not start yet).
func TestMapsAreAssignedFirst(t *testing.T) {
	c := newCoordinator([]string{"a.txt", "b.txt"}, 3)

	r := request(c, t0)
	if r.Type != MapTask || r.TaskID != 0 || r.File != "a.txt" || r.Attempt != 1 || r.NReduce != 3 {
		t.Fatalf("first request: got %+v", r)
	}

	r = request(c, t0)
	if r.Type != MapTask || r.TaskID != 1 || r.File != "b.txt" {
		t.Fatalf("second request: got %+v", r)
	}

	// Both maps are in progress and not late: no reduce task may start.
	r = request(c, t0)
	if r.Type != WaitTask {
		t.Fatalf("third request: want WaitTask, got %+v", r)
	}
}

// TestTimeoutReassignsTask checks the 10-second rule, including its
// boundary: exactly taskTimeout is not late yet, one nanosecond more is.
func TestTimeoutReassignsTask(t *testing.T) {
	c := newCoordinator([]string{"a.txt"}, 1)
	request(c, t0) // map 0 assigned at t0 (attempt 1)

	tests := []struct {
		name    string
		elapsed time.Duration // time since the assignment
		want    TaskType
	}{
		{"just assigned", 0, WaitTask},
		{"exactly at the timeout", taskTimeout, WaitTask},
		{"just after the timeout", taskTimeout + time.Nanosecond, MapTask},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := request(c, t0.Add(tc.elapsed))
			if r.Type != tc.want {
				t.Fatalf("after %v: want type %v, got %+v", tc.elapsed, tc.want, r)
			}
			if r.Type == MapTask && (r.TaskID != 0 || r.Attempt != 2) {
				t.Fatalf("re-assignment: want map 0 attempt 2, got %+v", r)
			}
		})
	}
}

// TestReassignedTaskRestartsTimer checks that a re-assigned task gets a
// fresh deadline: right after the re-assignment it is not late again.
func TestReassignedTaskRestartsTimer(t *testing.T) {
	c := newCoordinator([]string{"a.txt"}, 1)
	request(c, t0)                             // attempt 1 at t0
	late := t0.Add(taskTimeout + time.Second)  // attempt 1 is now late
	if r := request(c, late); r.Attempt != 2 { // attempt 2 starts at `late`
		t.Fatalf("want attempt 2, got %+v", r)
	}
	if r := request(c, late.Add(time.Second)); r.Type != WaitTask {
		t.Fatalf("attempt 2 is only 1s old: want WaitTask, got %+v", r)
	}
}

// TestReportCountsEachTaskOnce checks the rules for completion reports:
// the first report completes the task, whatever its attempt number, and
// later reports for the same task do not change the counters.
func TestReportCountsEachTaskOnce(t *testing.T) {
	c := newCoordinator([]string{"a.txt", "b.txt"}, 1)
	request(c, t0)                              // map 0, attempt 1
	request(c, t0)                              // map 1, attempt 1
	request(c, t0.Add(taskTimeout+time.Second)) // map 0 re-assigned, attempt 2

	report(t, c, MapTask, 0, 1) // the slow attempt 1 finishes first: accepted
	if c.mapsLeft != 1 {
		t.Fatalf("after first report: mapsLeft = %d, want 1", c.mapsLeft)
	}

	report(t, c, MapTask, 0, 2) // attempt 2 finishes too: duplicate, ignored
	report(t, c, MapTask, 0, 1) // a repeated report: ignored as well
	if c.mapsLeft != 1 {
		t.Fatalf("after duplicate reports: mapsLeft = %d, want 1", c.mapsLeft)
	}
}

// TestReportRejectsInvalidArgs checks that malformed reports are refused
// with an error and do not touch the state.
func TestReportRejectsInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args ReportTaskArgs
	}{
		{"wait is not a reportable type", ReportTaskArgs{Type: WaitTask, TaskID: 0}},
		{"exit is not a reportable type", ReportTaskArgs{Type: ExitTask, TaskID: 0}},
		{"negative task ID", ReportTaskArgs{Type: MapTask, TaskID: -1}},
		{"map ID too large", ReportTaskArgs{Type: MapTask, TaskID: 2}},
		{"reduce ID too large", ReportTaskArgs{Type: ReduceTask, TaskID: 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCoordinator([]string{"a.txt", "b.txt"}, 3)
			if err := c.ReportTask(&tc.args, &ReportTaskReply{}); err == nil {
				t.Fatalf("ReportTask(%+v): want an error, got nil", tc.args)
			}
			if c.mapsLeft != 2 || c.reducesLeft != 3 {
				t.Fatalf("state changed: mapsLeft=%d reducesLeft=%d", c.mapsLeft, c.reducesLeft)
			}
		})
	}
}

// TestFullJobSequence walks one job from start to finish, one task at a
// time, and checks the phase changes: maps, then reduces, then exit.
func TestFullJobSequence(t *testing.T) {
	c := newCoordinator([]string{"a.txt", "b.txt"}, 2)

	// Map phase.
	for i := 0; i < 2; i++ {
		r := request(c, t0)
		if r.Type != MapTask {
			t.Fatalf("map phase, request %d: got %+v", i, r)
		}
		report(t, c, MapTask, r.TaskID, r.Attempt)
	}

	// Reduce phase: reduce tasks need NMap to find their input files.
	for i := 0; i < 2; i++ {
		if c.Done() {
			t.Fatalf("Done() is true with %d reduce tasks left", 2-i)
		}
		r := request(c, t0)
		if r.Type != ReduceTask || r.TaskID != i || r.NMap != 2 {
			t.Fatalf("reduce phase, request %d: got %+v", i, r)
		}
		report(t, c, ReduceTask, r.TaskID, r.Attempt)
	}

	// Job finished.
	if !c.Done() {
		t.Fatal("Done() is false after all tasks completed")
	}
	if r := request(c, t0); r.Type != ExitTask {
		t.Fatalf("after the job: want ExitTask, got %+v", r)
	}
}

// TestDoneWithNoReduceTasks checks the corner case nReduce == 0:
// the job is not finished while map tasks are still pending.
func TestDoneWithNoReduceTasks(t *testing.T) {
	c := newCoordinator([]string{"a.txt"}, 0)
	if c.Done() {
		t.Fatal("Done() is true before the only map task ran")
	}
}

// TestConcurrentWorkersRunEachTaskOnce simulates many workers calling the
// RPC handlers at the same time (run it with -race). Without timeouts,
// every task must be assigned exactly once: this is the same property
// that test-mr.sh checks in its "job count" test.
func TestConcurrentWorkersRunEachTaskOnce(t *testing.T) {
	const nMap, nReduce, nWorkers = 20, 10, 8
	files := make([]string, nMap)
	for i := range files {
		files[i] = "input"
	}
	c := newCoordinator(files, nReduce)

	var mu sync.Mutex        // protects runs, which the workers share
	runs := map[[2]int]int{} // key: {task type, task ID}; value: times assigned
	var wg sync.WaitGroup

	for w := 0; w < nWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				reply := RequestTaskReply{}
				c.RequestTask(&RequestTaskArgs{}, &reply) // real handler, real clock
				switch reply.Type {
				case ExitTask:
					return
				case WaitTask:
					time.Sleep(time.Millisecond)
					continue
				}
				mu.Lock()
				runs[[2]int{int(reply.Type), reply.TaskID}]++
				mu.Unlock()
				args := ReportTaskArgs{Type: reply.Type, TaskID: reply.TaskID, Attempt: reply.Attempt}
				c.ReportTask(&args, &ReportTaskReply{})
			}
		}()
	}
	wg.Wait()

	if !c.Done() {
		t.Fatal("all workers exited but Done() is false")
	}
	if len(runs) != nMap+nReduce {
		t.Fatalf("%d distinct tasks ran, want %d", len(runs), nMap+nReduce)
	}
	for key, n := range runs {
		if n != 1 {
			t.Errorf("task %v was assigned %d times, want 1", key, n)
		}
	}
}