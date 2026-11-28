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

func (c *Coordinator) GetTask(args *GetTaskArgs, reply *GetTaskReply) error {
	c.taskLock.Lock()
	defer c.taskLock.Unlock()

	for {
		var taskType TaskType
		var taskID int

		switch {
		case len(c.remainingMapTasks) > 0:
			taskType = TaskMap
			taskID = firstTaskID(c.remainingMapTasks)

		case c.mapPhaseDone() && len(c.remainingReduceTasks) > 0:
			taskType = TaskReduce
			taskID = firstTaskID(c.remainingReduceTasks)

		case c.mapPhaseDone() && c.reducePhaseDone():
			reply.Task = Task{Type: TaskNone, WorkerID: args.WorkerID}
			return nil

		default:
			c.waitingForTaskCond.Wait()
			continue
		}

		task := c.assignTask(taskType, taskID, args.WorkerID)
		reply.Task = task
		c.startTaskTimeout(taskType, taskID)
		return nil
	}
}

func firstTaskID(tasks map[int]struct{}) int {
	for id := range tasks {
		return id
	}
	return -1
}

func (c *Coordinator) taskSets(taskType TaskType) (
	map[int]Task,
	map[int]struct{},
	map[int]struct{},
) {
	switch taskType {
	case TaskMap:
		return c.mapTasks, c.remainingMapTasks, c.startedMapTasks
	case TaskReduce:
		return c.reduceTasks, c.remainingReduceTasks, c.startedReduceTasks
	default:
		panic("unknown task type")
	}
}

func (c *Coordinator) assignTask(taskType TaskType, taskID, workerID int) Task {
	tasks, remaining, started := c.taskSets(taskType)

	task := tasks[taskID]
	task.WorkerID = workerID
	task.Status = TaskInProgress

	delete(remaining, taskID)
	started[taskID] = struct{}{}
	tasks[taskID] = task
	return task
}

func (c *Coordinator) startTaskTimeout(taskType TaskType, taskID int) {
	time.AfterFunc(10*time.Second, func() {
		c.taskLock.Lock()
		defer c.taskLock.Unlock()

		tasks, remaining, started := c.taskSets(taskType)
		task, ok := tasks[taskID]
		if !ok {
			return
		}
		if task.Status != TaskInProgress && task.Status != TaskWaitAck {
			return
		}

		task.Status = TaskIdle
		task.WorkerID = -1
		tasks[taskID] = task
		delete(started, taskID)
		remaining[taskID] = struct{}{}
		c.waitingForTaskCond.Broadcast()
	})
}

func (c *Coordinator) ReportTaskCompletion(
	args *TaskCompletionArgs,
	reply *TaskCompletionReply,
) error {
	c.taskLock.Lock()
	defer c.taskLock.Unlock()

	tasks, _, _ := c.taskSets(args.TaskType)
	task, ok := tasks[args.TaskID]
	if !ok || task.WorkerID != args.WorkerID {
		reply.Approved = false
		return nil
	}

	if task.Status == TaskWaitAck {
		reply.Approved = true
		return nil
	}
	if task.Status != TaskInProgress {
		reply.Approved = false
		return nil
	}

	task.Status = TaskWaitAck
	tasks[args.TaskID] = task
	reply.Approved = true
	return nil
}

func (c *Coordinator) AckTaskCompletion(
	args *AckTaskCompletionArgs,
	reply *AckTaskCompletionReply,
) error {
	c.taskLock.Lock()
	defer c.taskLock.Unlock()

	tasks, _, started := c.taskSets(args.TaskType)
	task, ok := tasks[args.TaskID]
	if !ok || task.WorkerID != args.WorkerID {
		reply.Accepted = false
		return nil
	}

	if task.Status == TaskCompleted {
		reply.Accepted = true
		return nil
	}
	if task.Status != TaskWaitAck {
		reply.Accepted = false
		return nil
	}

	task.Status = TaskCompleted
	tasks[args.TaskID] = task
	delete(started, args.TaskID)
	reply.Accepted = true
	c.waitingForTaskCond.Broadcast()
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
	c.taskLock.Lock()
	defer c.taskLock.Unlock()
	return c.mapPhaseDone() && c.reducePhaseDone()
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
