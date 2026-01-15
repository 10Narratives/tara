package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/10Narratives/faas/internal/app/components/nats"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Task struct {
	TaskID        string        `json:"task_id"`
	FunctionID    string        `json:"function_id"`
	ExecutionTime time.Duration `json:"execution_time"`
}

var (
	count    int
	minTime  time.Duration
	maxTime  time.Duration
	waitTime time.Duration
	function string
)

func init() {
	flag.IntVar(&count, "count", 0, "количество задач (0=бесконечно)")
	flag.DurationVar(&minTime, "min-time", 1*time.Millisecond, "мин. время выполнения")
	flag.DurationVar(&maxTime, "max-time", 1*time.Second, "макс. время выполнения")
	flag.DurationVar(&waitTime, "wait", 100*time.Millisecond, "пауза между задачами")
	flag.StringVar(&function, "function", "my-function", "имя функции для отправки задач")
	flag.Parse()
}

func main() {
	natsURL := "nats://localhost:4222"
	if url := os.Getenv("NATS_URL"); url != "" {
		natsURL = url
	}

	if minTime >= maxTime {
		fmt.Printf("error: min-time (%v) must be < max-time (%v)\n", minTime, maxTime)
		os.Exit(1)
	}

	logger := setupLogger()
	defer logger.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	us, err := nats.NewUnifiedStorage(natsURL)
	if err != nil {
		logger.Fatal("nats connect failed", zap.Error(err))
	}
	defer us.Conn.Close()

	js, err := jetstream.New(us.Conn)
	if err != nil {
		logger.Fatal("jetstream failed", zap.Error(err))
	}

	logger.Info("task publisher ready",
		zap.Int("count", count),
		zap.Duration("min-time", minTime),
		zap.Duration("max-time", maxTime),
		zap.Duration("wait", waitTime),
		zap.String("function", function))

	rand.Seed(time.Now().UnixNano())
	sent := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if count > 0 && sent >= count {
				logger.Info("all tasks sent", zap.Int("total", sent))
				return
			}

			if err := publishTask(ctx, js, logger, function); err != nil {
				logger.Warn("publish failed", zap.Error(err))
			}
			sent++
			logger.Info("sent",
				zap.Int("task_num", sent),
				zap.Int("remaining", count-sent),
				zap.String("function", function))

			time.Sleep(waitTime)
		}
	}
}

func publishTask(ctx context.Context, js jetstream.JetStream, log *zap.Logger, functionName string) error {
	execTime := minTime + time.Duration(rand.Int63n(int64(maxTime-minTime)))

	task := Task{
		TaskID:        fmt.Sprintf("task-%d", time.Now().UnixNano()),
		FunctionID:    functionName,
		ExecutionTime: execTime.Round(time.Millisecond),
	}

	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

	log.Debug("publishing task",
		zap.String("task_id", task.TaskID),
		zap.Duration("execution_time", task.ExecutionTime),
		zap.String("function", functionName))

	subject := fmt.Sprintf("task.%s", functionName)
	_, err = js.Publish(ctx, subject, payload)
	if err != nil {
		return fmt.Errorf("publish task to %s: %w", subject, err)
	}

	hint := []byte(subject)
	_, err = js.Publish(ctx, "task.hints", hint)
	if err != nil {
		return fmt.Errorf("publish hint for %s: %w", subject, err)
	}

	log.Debug("published hint",
		zap.String("hint_subject", string(hint)),
		zap.String("function", functionName))

	return nil
}

func setupLogger() *zap.Logger {
	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.Level = zap.NewAtomicLevelAt(zap.DebugLevel) // Добавлен debug-уровень для тестирования
	logger, err := config.Build()
	if err != nil {
		log.Fatal("logger setup failed:", err)
	}
	return logger
}
