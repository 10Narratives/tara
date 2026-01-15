package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Task struct {
	TaskID        string        `json:"task_id"`
	FunctionID    string        `json:"function_id"`
	ExecutionTime time.Duration `json:"execution_time"`
}

type InstanceConfig struct {
	FunctionName string
	ColdStart    time.Duration
	Lifetime     time.Duration
}

type Instance struct {
	functionName string
	startTime    time.Time
	stopTime     time.Time
	log          *zap.Logger
	done         chan struct{}
	mu           sync.RWMutex
}

func NewInstance(log *zap.Logger, cfg *InstanceConfig) (*Instance, error) {
	time.Sleep(cfg.ColdStart)

	inst := &Instance{
		functionName: cfg.FunctionName,
		startTime:    time.Now(),
		stopTime:     time.Now().Add(cfg.Lifetime),
		log:          log,
		done:         make(chan struct{}),
	}

	return inst, nil
}

func (i *Instance) Run(ctx context.Context) error {
	i.log.Info("instance started",
		zap.String("function_name", i.functionName),
		zap.Time("start_time", i.startTime),
		zap.Time("stop_time", i.stopTime),
	)

	select {
	case <-ctx.Done():
		i.log.Info("instance context cancelled")
		return ctx.Err()
	case <-time.After(time.Until(i.stopTime)):
		i.log.Info("instance lifetime ended",
			zap.String("function_name", i.functionName),
		)
		close(i.done)
		return nil
	}
}

func (i *Instance) Stop(ctx context.Context) error {
	select {
	case <-i.done:
		return nil
	default:
		i.log.Info("instance stopped manually",
			zap.String("function_name", i.functionName),
		)
		close(i.done)
		return nil
	}
}

func (i *Instance) isAlive() bool {
	select {
	case <-i.done:
		return false
	default:
		return time.Now().Before(i.stopTime)
	}
}

func (i *Instance) Execute(ctx context.Context, task *Task) error {
	if !i.isAlive() {
		return fmt.Errorf("instance is not alive")
	}

	select {
	case <-time.After(task.ExecutionTime):
		i.log.Info("task done",
			zap.String("function_name", task.FunctionID),
			zap.String("task_name", task.TaskID),
			zap.Duration("execution_time", task.ExecutionTime),
		)
		return nil
	case <-ctx.Done():
		i.log.Info("task cancelled",
			zap.String("function_name", task.FunctionID),
			zap.String("task_name", task.TaskID),
			zap.Error(ctx.Err()),
		)
		return ctx.Err()
	case <-i.done:
		return fmt.Errorf("instance stopped")
	}
}
