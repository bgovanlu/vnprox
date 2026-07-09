package pvemock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Task mirrors a PVE async task, addressed by a UPID-style identifier and
// polled via GET /nodes/{node}/tasks/{upid}/status until Status != "running".
type Task struct {
	StartTime  time.Time
	EndTime    time.Time
	UPID       string
	Node       string
	Type       string
	User       string
	Status     string
	ExitStatus string
	log        []string
	mu         sync.Mutex
}

func (t *Task) appendLog(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = append(t.log, line)
}

func (t *Task) logLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.log...)
}

type taskManager struct {
	tasks map[string]*Task
	clock func() time.Time
	seq   atomic.Uint64
	mu    sync.Mutex
}

func newTaskManager(clock func() time.Time) *taskManager {
	if clock == nil {
		clock = time.Now
	}
	return &taskManager{tasks: make(map[string]*Task), clock: clock}
}

func (tm *taskManager) nextUPID(node, taskType, id, user string) string {
	n := tm.seq.Add(1)
	// UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>:
	return fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:%s:", node, n, n, tm.clock().Unix(), taskType, id, user)
}

// Run starts a task that completes asynchronously after latency, then
// invokes apply(!fail) — apply is responsible for committing or rolling
// back whatever state the task represents. It returns the UPID immediately,
// matching PVE's fire-and-poll async task model.
func (tm *taskManager) Run(node, taskType, id, user string, latency time.Duration, fail bool, failReason string, apply func(success bool)) *Task {
	t := &Task{
		UPID:      tm.nextUPID(node, taskType, id, user),
		Node:      node,
		Type:      taskType,
		User:      user,
		StartTime: tm.clock(),
		Status:    "running",
	}
	t.appendLog(fmt.Sprintf("starting task %s", t.UPID))

	tm.mu.Lock()
	tm.tasks[t.UPID] = t
	tm.mu.Unlock()

	run := func() {
		if latency > 0 {
			time.Sleep(latency)
		}
		if apply != nil {
			apply(!fail)
		}
		t.mu.Lock()
		t.EndTime = tm.clock()
		t.Status = "stopped"
		if fail {
			reason := failReason
			if reason == "" {
				reason = ErrTaskFailed.Error()
			}
			t.ExitStatus = "failed: " + reason
		} else {
			t.ExitStatus = "OK"
		}
		t.mu.Unlock()
		t.appendLog("TASK " + t.ExitStatus)
	}
	if latency > 0 {
		go run()
	} else {
		// Zero latency still runs "asynchronously" in the sense that the
		// caller gets a UPID back and must poll, but there is no reason
		// to spawn a goroutine (and doing so makes tests race on
		// completion timing for no benefit).
		run()
	}
	return t
}

func (tm *taskManager) Get(upid string) (*Task, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t, ok := tm.tasks[upid]
	return t, ok
}
