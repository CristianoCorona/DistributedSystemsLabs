package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

// taskTimeout is how long the coordinator waits for a task before
// assuming its worker failed and handing the task to someone else.
const taskTimeout = 10 * time.Second

// taskState is the position of a task in its life cycle:
// idle -> inProgress -> completed, with inProgress -> idle on timeout.
type taskState int

const (
	idle       taskState = iota // not assigned, or assigned and given up on (iota to start from zero)
	inProgress                  // assigned to a worker that has not reported yet
	completed                   // output files are in their final place
)

// task is the coordinator's record for one map or one reduce task.
// It is unexported (lowercase): workers never see it, they only see RPC replies.
type task struct {
	state   taskState // current position in the life cycle
	started time.Time // when the current attempt was assigned (inProgress only)
	attempt int       // number of times the task has been assigned so far
	file    string    // map tasks only: the input file
}

// Coordinator holds the state of the whole job.
// Every field below mu is protected by mu: RPC handlers run concurrently,
// one goroutine per call, so every access must hold the lock.
type Coordinator struct {
	mu          sync.Mutex
	mapTasks    []task // one per input file; index = map task number X
	reduceTasks []task // nReduce of them; index = reduce task number Y
	mapsLeft    int    // map tasks not yet completed
	reducesLeft int    // reduce tasks not yet completed
}

// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
// RequestTask is the RPC handler called by a worker that wants work.
// It only reads the clock and delegates to assignTask, so that the
// scheduling logic can be tested with a fake time and without RPC.
func (c *Coordinator) RequestTask(args *RequestTaskArgs, reply *RequestTaskReply) error {
	c.assignTask(time.Now(), reply)
	return nil
}

// assignTask fills reply with the next thing the worker should do:
// a map task, a reduce task, a wait, or an exit.
// Reduce tasks are only handed out once every map task has completed,
// because each reduce task needs the output of all map tasks.
func (c *Coordinator) assignTask(now time.Time, reply *RequestTaskReply) {
	c.mu.Lock()
	defer c.mu.Unlock()
 
	// Map phase: some map output is still missing.
	if c.mapsLeft > 0 {
		i := pick(c.mapTasks, now)
		if i < 0 {
			// Every unfinished map task is running and not yet late:
			// reduce tasks cannot start, so the worker has to wait.
			reply.Type = WaitTask
			return
		}
		startAttempt(&c.mapTasks[i], now) // pointer: modify the task inside the slice, no copy
		reply.Type = MapTask
		reply.TaskID = i
		reply.Attempt = c.mapTasks[i].attempt
		reply.File = c.mapTasks[i].file
		reply.NReduce = len(c.reduceTasks)
		return
	}
 
	// Reduce phase: all map output exists, some reduce output is missing.
	if c.reducesLeft > 0 {
		i := pick(c.reduceTasks, now)
		if i < 0 {
			// Every unfinished reduce task is running and not yet late.
			reply.Type = WaitTask
			return
		}
		startAttempt(&c.reduceTasks[i], now)
		reply.Type = ReduceTask
		reply.TaskID = i
		reply.Attempt = c.reduceTasks[i].attempt
		reply.NMap = len(c.mapTasks)
		return
	}
 
	// Nothing left to do: tell the worker to terminate.
	reply.Type = ExitTask
}

// pick returns the index of a task that can be assigned right now,
// or -1 if there is none. A task can be assigned if it is idle, or if it
// has been in progress for longer than taskTimeout (its worker is presumed failed).
// Timeouts are detected lazily here, only when a worker asks for work,
// so no background goroutine is needed.
// The caller must hold c.mu.
func pick(tasks []task, now time.Time) int {
	for i := range tasks { // iterate by index: we only read, but avoid copying each struct
		switch tasks[i].state {
		case idle:
			return i
		case inProgress:
			if now.Sub(tasks[i].started) > taskTimeout {
				return i // late: re-issue it to the asking worker
			}
		}
		// completed tasks are skipped
	}
	return -1
}

// startAttempt marks t as assigned at time now.
// It takes a pointer because it must modify the task stored in the slice,
// not a copy of it. The caller must hold c.mu (as done in assignTask).
func startAttempt(t *task, now time.Time) {
	t.state = inProgress
	t.started = now
	t.attempt++ // first assignment -> attempt 1
}

// ReportTask is the RPC handler called by a worker whose task is done,
// i.e. whose output files have all been renamed to their final names.
//
// The first report for a task marks it completed; later reports for the
// same task are ignored. A report is accepted whatever its Attempt number:
// even if the task was re-assigned in the meantime, the reporting worker's
// files are complete, and map/reduce are deterministic, so every attempt
// produces the same bytes.
func (c *Coordinator) ReportTask(args *ReportTaskArgs, reply *ReportTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()
 
	// Select the task list and the counter that this report refers to.
	var tasks []task
	var left *int // pointer, so we can decrement the counter below
	switch args.Type {
	case MapTask:
		tasks, left = c.mapTasks, &c.mapsLeft
	case ReduceTask:
		tasks, left = c.reduceTasks, &c.reducesLeft
	default:
		return fmt.Errorf("ReportTask: unexpected task type %v", args.Type)
	}
 
	// Reject IDs that do not exist: that would be a bug in the worker.
	if args.TaskID < 0 || args.TaskID >= len(tasks) {
		return fmt.Errorf("ReportTask: task ID %d out of range", args.TaskID)
	}
 
	t := &tasks[args.TaskID] // tasks shares its backing array with the Coordinator's slice
	if t.state == completed {
		return nil // duplicate report: the task was already counted
	}
	t.state = completed
	*left-- // decremented exactly once per task, thanks to the check above
	return nil
}


//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
// It reads shared state, so it
// takes the lock like any RPC handler.
func (c *Coordinator) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mapsLeft == 0 && c.reducesLeft == 0
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
// newCoordinator builds the initial job state: one idle map task per
// input file and nReduce idle reduce tasks. It does not start the RPC
// server, so tests can create coordinators freely.
func newCoordinator(files []string, nReduce int) *Coordinator {
	c := &Coordinator{
		mapTasks:    make([]task, len(files)), // zero value of task = idle, attempt 0
		reduceTasks: make([]task, nReduce),
		mapsLeft:    len(files),
		reducesLeft: nReduce,
	}
	for i, f := range files {
		c.mapTasks[i].file = f // map task i reads input file i
	}
	return c
}
 
// MakeCoordinator creates a Coordinator and starts serving RPCs.
// main/mrcoordinator.go calls this function; nReduce is the number of
// reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := newCoordinator(files, nReduce)
	c.server()
	return c
}