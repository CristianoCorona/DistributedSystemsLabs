package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import "os"
import "strconv"

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// Add your RPC definitions here.

// TaskType tells a worker what to do with the reply it received.
type TaskType int 

const (
	// WaitTask is the zero value: a reply whose Type was never set (e.g. bug in the
	// coordinator) makes the worker sleep and ask again, causing no harm.
	WaitTask TaskType = iota     // iota is an identifier so that this sequence starts from zero
	MapTask 					 // run Map on one input file
	ReduceTask					 // run Reduce on one bucket of intermediate files
	ExitTask 					 // the job is finished; the worker can terminate
)

// 1. Worker Request a task.
// RequestTaskArgs is sent by a worker that is ready for new work.
// No data (basic part); fields can be added in the advanced part if needed,
// without changing the RPC signature
type RequestTaskArgs struct{}

//2. Coordinator assigns back a task.
// RequestTaskReply describes the task assigned to the worker.
// Only the fields relevant to Type are meaningful.
type RequestTaskReply struct {
	Type    TaskType // what kind of task this is
	TaskID  int      // index of the task: X for map tasks, Y for reduce tasks
	Attempt int      // how many times this task has been assigned (1 = first time)
	File    string   // map tasks only: the input file to read
	NMap    int      // reduce tasks only: how many mr-X-Y files to read
	NReduce int      // map tasks only: how many buckets to split the output into
}

// 3. Worker report task completion.
// ReportTaskArgs is sent by a worker after its task's output files
// have all been written and renamed to their final names.
type ReportTaskArgs struct {
	Type    TaskType // MapTask or ReduceTask
	TaskID  int      // the TaskID received in RequestTaskReply
	Attempt int      // the Attempt received in RequestTaskReply
}

// ReportTaskReply is empty: the worker only needs to know the call succeeded (ACK).
type ReportTaskReply struct{}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}