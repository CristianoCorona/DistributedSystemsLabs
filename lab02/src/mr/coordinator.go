package mr

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type TaskType string

const (
	TaskMap    TaskType = "Map"
	TaskReduce TaskType = "Reduce"
	TaskNone   TaskType = "None"
)

type TaskStatus string

const (
	TaskIdle       TaskStatus = "Idle"
	TaskInProgress TaskStatus = "InProgress"
	TaskWaitAck    TaskStatus = "WaitAck"
	TaskCompleted  TaskStatus = "Completed"
	TaskNotReady   TaskStatus = "NotReady"
)

type Task struct {
	Type     TaskType
	Status   TaskStatus
	WorkerID int
	TaskID   int
	// For Map tasks, this will contain the input file name.
	InputFile string
	NReduce   int
	NMap      int
}

type Coordinator struct {
	mapTasks             map[int]Task
	reduceTasks          map[int]Task
	startedMapTasks      map[int]struct{}
	startedReduceTasks   map[int]struct{}
	remainingMapTasks    map[int]struct{}
	remainingReduceTasks map[int]struct{}
	nReduce              int
	nextWorkerID         int
	registrationLock     sync.Mutex
	taskLock             sync.Mutex
	waitingForTaskCond   *sync.Cond
}

func (c *Coordinator) RegisterWorker(args *RegisterWorkerArgs, reply *RegisterWorkerReply) error {
	c.registrationLock.Lock()
	workerID := c.nextWorkerID
	c.nextWorkerID++
	c.registrationLock.Unlock()
	reply.WorkerID = workerID
	log.Printf("RegisterWorker called. Assigned WorkerID: %d\n", workerID)
	return nil
}

// Your code here -- RPC handlers for the worker to call.
func (c *Coordinator) GetTask(args *GetTaskArgs, reply *GetTaskReply) error {
	log.Printf("GetTask called, WorkerID: %d\n", args.WorkerID)

	c.taskLock.Lock()
	defer c.taskLock.Unlock()
	for (len(c.remainingMapTasks) == 0 && len(c.startedMapTasks) > 0) ||
		(len(c.remainingReduceTasks) == 0 && len(c.startedReduceTasks) > 0) {
		log.Printf("Waiting for tasks to become available for Worker %d\n", args.WorkerID)
		c.waitingForTaskCond.Wait()
	}
	switch {
	case len(c.remainingMapTasks) > 0:
		taskID := -1
		for id := range c.remainingMapTasks {
			taskID = id
			break
		}
		delete(c.remainingMapTasks, taskID)
		c.startedMapTasks[taskID] = struct{}{}
		task := c.mapTasks[taskID]
		task.WorkerID = args.WorkerID
		task.Status = TaskInProgress
		c.mapTasks[taskID] = task
		log.Printf("Assigning Map task %d to Worker %d\n", taskID, args.WorkerID)
		reply.Task = task
		time.AfterFunc(10*time.Second, func() {
			c.taskLock.Lock()
			defer c.taskLock.Unlock()
			if task, ok := c.mapTasks[taskID]; ok {
				if task.Status == TaskInProgress {
					log.Printf("Map task %d timed out. Reassigning...\n", taskID)
					delete(c.startedMapTasks, taskID)
					task.WorkerID = -1
					task.Status = TaskIdle
					c.mapTasks[taskID] = task
					c.remainingMapTasks[taskID] = struct{}{}
					c.waitingForTaskCond.Signal()
				}
			}
		})
	//case len(c.startedMapTasks) > 0:
	// TODO: Handle in-progress map tasks (e.g., check for timeouts)
	case len(c.remainingReduceTasks) > 0:
		// for len(c.startedMapTasks) > 0 {
		// 	c.taskLock.Unlock()
		// 	time.Sleep(500 * time.Millisecond)
		// 	c.taskLock.Lock()
		// }
		taskID := -1
		for id := range c.remainingReduceTasks {
			taskID = id
			break
		}
		delete(c.remainingReduceTasks, taskID)
		c.startedReduceTasks[taskID] = struct{}{}
		task := c.reduceTasks[taskID]
		task.WorkerID = args.WorkerID
		task.Status = TaskInProgress
		c.reduceTasks[taskID] = task
		log.Printf("Assigning Reduce task %d to Worker %d\n", taskID, args.WorkerID)
		reply.Task = task
		time.AfterFunc(10*time.Second, func() {
			c.taskLock.Lock()
			defer c.taskLock.Unlock()
			if task, ok := c.reduceTasks[taskID]; ok {
				if task.Status == TaskInProgress {
					log.Printf("Reduce task %d timed out. Reassigning...\n", taskID)
					delete(c.startedReduceTasks, taskID)
					task.WorkerID = -1
					task.Status = TaskIdle
					c.reduceTasks[taskID] = task
					c.remainingReduceTasks[taskID] = struct{}{}
					c.waitingForTaskCond.Signal()
				}
			}
		})
	default:
		log.Printf("No tasks available for Worker %d\n", args.WorkerID)
		reply.Task = Task{
			Type:     TaskNone,
			WorkerID: args.WorkerID,
		}
	}

	return nil
}

func (c *Coordinator) ReportTaskCompletion(args *TaskCompletionArgs, reply *TaskCompletionReply) error {

	taskID := args.TaskID
	taskType := args.TaskType

	c.taskLock.Lock()
	defer c.taskLock.Unlock()
	log.Printf("ReportTaskCompletion called, WorkerID: %d, TaskID: %d, TaskType: %s\n", args.WorkerID, args.TaskID, args.TaskType)
	log.Printf("Remaining Map Tasks: %d, Remaining Reduce Tasks: %d\n", len(c.remainingMapTasks), len(c.remainingReduceTasks))

	switch taskType {
	case TaskMap:
		if task, ok := c.mapTasks[taskID]; ok {
			if task.WorkerID != args.WorkerID {
				log.Printf("Worker %d reported completion for Map task %d, but it was assigned to Worker %d\n", args.WorkerID, taskID, task.WorkerID)
				reply.Approved = false
				return nil
			}
			task.Status = TaskWaitAck
			c.mapTasks[taskID] = task
		}
	case TaskReduce:
		if task, ok := c.reduceTasks[taskID]; ok {
			if task.WorkerID != args.WorkerID {
				log.Printf("Worker %d reported completion for Reduce task %d, but it was assigned to Worker %d\n", args.WorkerID, taskID, task.WorkerID)
				reply.Approved = false
				return nil
			}
			task.Status = TaskWaitAck
			c.reduceTasks[taskID] = task
		}
	}

	reply.Approved = true
	if c.mapPhaseDone() || c.reducePhaseDone() {
		c.waitingForTaskCond.Broadcast()
	}
	return nil
}

func (c *Coordinator) AckTaskCompletion(args *AckTaskCompletionArgs, reply *AckTaskCompletionReply) error {
	// Implementation for acknowledging task completion
	taskID := args.TaskID
	taskType := args.TaskType

	c.taskLock.Lock()
	defer c.taskLock.Unlock()
	log.Printf("AckTaskCompletion called, WorkerID: %d, TaskID: %d, TaskType: %s\n", args.WorkerID, args.TaskID, args.TaskType)

	switch taskType {
	case TaskMap:
		if task, ok := c.mapTasks[taskID]; ok {
			if task.WorkerID != args.WorkerID {
				log.Printf("Worker %d acknowledged completion for Map task %d, but it was assigned to Worker %d\n", args.WorkerID, taskID, task.WorkerID)
				return nil
			}
			task.Status = TaskCompleted
			c.mapTasks[taskID] = task
			delete(c.startedMapTasks, taskID)
		}
	case TaskReduce:
		if task, ok := c.reduceTasks[taskID]; ok {
			if task.WorkerID != args.WorkerID {
				log.Printf("Worker %d acknowledged completion for Reduce task %d, but it was assigned to Worker %d\n", args.WorkerID, taskID, task.WorkerID)
				return nil
			}
			task.Status = TaskCompleted
			c.reduceTasks[taskID] = task
			delete(c.startedReduceTasks, taskID)
		}
	}

	return nil
}

// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

func (c *Coordinator) mapPhaseDone() bool {
	return len(c.remainingMapTasks) == 0 && len(c.startedMapTasks) == 0
}

func (c *Coordinator) reducePhaseDone() bool {
	return len(c.remainingReduceTasks) == 0 && len(c.startedReduceTasks) == 0
}

// start a thread that listens for RPCs from worker.go
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

// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	ret := false

	c.taskLock.Lock()
	defer c.taskLock.Unlock()
	if len(c.remainingMapTasks) == 0 &&
		len(c.remainingReduceTasks) == 0 &&
		len(c.startedMapTasks) == 0 &&
		len(c.startedReduceTasks) == 0 {
		ret = true
	}

	return ret
}

// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{
		mapTasks:             make(map[int]Task),
		reduceTasks:          make(map[int]Task),
		remainingMapTasks:    make(map[int]struct{}, len(files)),
		remainingReduceTasks: make(map[int]struct{}, nReduce),
		startedMapTasks:      make(map[int]struct{}, len(files)),
		startedReduceTasks:   make(map[int]struct{}, nReduce),
		nReduce:              nReduce,
		nextWorkerID:         1,
		registrationLock:     sync.Mutex{},
		taskLock:             sync.Mutex{},
	}

	c.waitingForTaskCond = sync.NewCond(&c.taskLock)

	for i, file := range files {
		c.mapTasks[i] = Task{
			Type:      TaskMap,
			Status:    TaskIdle,
			WorkerID:  -1,
			TaskID:    i,
			InputFile: file,
			NReduce:   nReduce,
			NMap:      len(files),
		}
		c.remainingMapTasks[i] = struct{}{}
	}

	for i := 0; i < nReduce; i++ {
		c.reduceTasks[i] = Task{
			Type:     TaskReduce,
			Status:   TaskNotReady,
			WorkerID: -1,
			TaskID:   i,
			NReduce:  nReduce,
			NMap:     len(files),
		}
		c.remainingReduceTasks[i] = struct{}{}
	}

	if os.Getenv("DEBUG") != "1" {
		log.SetOutput(io.Discard)
	}

	c.server()
	return &c
}
