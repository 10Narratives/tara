package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/10Narratives/faas/internal/app/components/nats"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	managerActiveInstances = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "faas",
		Subsystem: "manager",
		Name:      "active_instances",
		Help:      "Number of active function instances",
	}, []string{"pod", "function"})

	managerTotalInstances = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "faas",
		Subsystem: "manager",
		Name:      "total_instances",
		Help:      "Total number of function instances (active + inactive)",
	}, []string{"pod"})

	managerInstanceCreationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "manager",
		Name:      "instance_creations_total",
		Help:      "Total number of instance creations",
	}, []string{"pod", "function"})

	managerInstanceDeletionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "manager",
		Name:      "instance_deletions_total",
		Help:      "Total number of instance deletions",
	}, []string{"pod", "function"})

	consumerMessagesFetchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "consumer",
		Name:      "messages_fetched_total",
		Help:      "Total number of messages fetched from queues",
	}, []string{"pod", "consumer_type", "function"})

	consumerMessagesProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "consumer",
		Name:      "messages_processed_total",
		Help:      "Total number of messages successfully processed",
	}, []string{"pod", "function"})

	consumerMessagesFailedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "consumer",
		Name:      "messages_failed_total",
		Help:      "Total number of messages that failed processing",
	}, []string{"pod", "function", "reason"})

	consumerPollEmptyTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "consumer",
		Name:      "poll_empty_total",
		Help:      "Total number of empty polls",
	}, []string{"pod", "consumer_type", "function"})

	taskExecutionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "faas",
		Subsystem: "task",
		Name:      "execution_duration_seconds",
		Help:      "Task execution duration in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"pod", "function"})

	taskPayloadSizeBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "faas",
		Subsystem: "task",
		Name:      "payload_size_bytes",
		Help:      "Task payload size in bytes",
		Buckets:   []float64{256, 1024, 4096, 16384, 65536, 262144},
	}, []string{"pod", "function"})

	managerMaxInstancesReachedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "faas",
		Subsystem: "manager",
		Name:      "max_instances_reached_total",
		Help:      "Total number of times max instances limit was reached",
	}, []string{"pod"})
)

type ManagerConfig struct {
	MaxInstances     int
	InstanceLifetime time.Duration
	ColdStart        time.Duration
	NATSURL          string
	PodName          string
	MaxAckPending    int
	AckWait          time.Duration
	MaxDeliver       int
	Backoff          []time.Duration
}

type Manager struct {
	cfg    *ManagerConfig
	log    *zap.Logger
	us     *nats.UnifiedStorage
	instMu sync.RWMutex
	insts  map[string]*Instance
}

func NewManager(log *zap.Logger, cfg *ManagerConfig) (*Manager, error) {
	if cfg.PodName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("get hostname: %w", err)
		}
		cfg.PodName = fmt.Sprintf("faas-agent-%s", hostname)
		log.Info("generated pod name", zap.String("pod_name", cfg.PodName))
	}

	us, err := nats.NewUnifiedStorage(cfg.NATSURL)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	// Register pod-level metrics on startup
	managerTotalInstances.WithLabelValues(cfg.PodName).Set(0)

	log.Info("manager created",
		zap.String("nats", cfg.NATSURL),
		zap.String("pod_name", cfg.PodName))

	return &Manager{
		cfg:   cfg,
		log:   log,
		us:    us,
		insts: make(map[string]*Instance),
	}, nil
}

func (m *Manager) Run(ctx context.Context) error {
	m.log.Info("manager started", zap.Int("max_instances", m.cfg.MaxInstances))

	// Update max instances metric
	managerTotalInstances.WithLabelValues(m.cfg.PodName).Set(float64(m.cfg.MaxInstances))

	hintsCtx, hintsCancel := context.WithCancel(ctx)
	hintsErr := make(chan error, 1)

	go func() {
		defer hintsCancel()
		m.log.Info("hints consumer goroutine started")
		hintsErr <- m.runHintsConsumer(hintsCtx)
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.log.Info("manager context cancelled")
			return ctx.Err()
		case err := <-hintsErr:
			return fmt.Errorf("hints consumer failed: %w", err)
		case <-ticker.C:
			m.updateInstanceMetrics()
		}
	}
}

func (m *Manager) updateInstanceMetrics() {
	m.instMu.RLock()
	defer m.instMu.RUnlock()

	aliveCount := 0
	functionStats := make(map[string]float64)

	for fn, inst := range m.insts {
		if inst.isAlive() {
			aliveCount++
			functionStats[fn] = 1
			managerActiveInstances.WithLabelValues(m.cfg.PodName, fn).Set(1)
		} else {
			managerActiveInstances.WithLabelValues(m.cfg.PodName, fn).Set(0)
		}
	}

	managerTotalInstances.WithLabelValues(m.cfg.PodName).Set(float64(len(m.insts)))
}

func (m *Manager) logInstStats() {
	m.instMu.RLock()
	alive := 0
	for fn := range m.insts {
		if m.insts[fn].isAlive() {
			alive++
		}
	}
	m.instMu.RUnlock()
	m.log.Debug("instance stats",
		zap.Int("alive", alive),
		zap.Int("total", len(m.insts)),
		zap.String("pod", m.cfg.PodName))
}

func (m *Manager) Stop(ctx context.Context) error {
	m.log.Info("manager stopping", zap.String("pod", m.cfg.PodName))
	m.instMu.Lock()
	for fn, inst := range m.insts {
		m.log.Info("stopping instance",
			zap.String("pod", m.cfg.PodName),
			zap.String("function", fn))
		_ = inst.Stop(ctx)
		managerInstanceDeletionsTotal.WithLabelValues(m.cfg.PodName, fn).Inc()
	}
	m.instMu.Unlock()

	if m.us != nil {
		m.us.Conn.Close()
	}
	m.log.Info("manager stopped", zap.String("pod", m.cfg.PodName))
	return nil
}

func (m *Manager) getOrCreateInstance(fnName string) (*Instance, error) {
	m.instMu.RLock()
	if inst, ok := m.insts[fnName]; ok && inst.isAlive() {
		m.instMu.RUnlock()
		m.log.Debug("reuse existing instance",
			zap.String("pod", m.cfg.PodName),
			zap.String("function", fnName))
		return inst, nil
	}
	m.instMu.RUnlock()

	m.instMu.Lock()
	defer m.instMu.Unlock()

	if inst, ok := m.insts[fnName]; ok && inst.isAlive() {
		m.log.Debug("reuse existing instance (double check)",
			zap.String("pod", m.cfg.PodName),
			zap.String("function", fnName))
		return inst, nil
	}

	aliveCount := 0
	for _, inst := range m.insts {
		if inst.isAlive() {
			aliveCount++
		}
	}
	if aliveCount >= m.cfg.MaxInstances {
		managerMaxInstancesReachedTotal.WithLabelValues(m.cfg.PodName).Inc()
		return nil, fmt.Errorf("max instances reached: %d", m.cfg.MaxInstances)
	}

	m.log.Info("creating new instance",
		zap.String("pod", m.cfg.PodName),
		zap.String("function", fnName),
		zap.Duration("lifetime", m.cfg.InstanceLifetime),
		zap.Duration("cold_start", m.cfg.ColdStart),
		zap.Int("current_alive", aliveCount))

	instCfg := &InstanceConfig{
		FunctionName: fnName,
		ColdStart:    m.cfg.ColdStart,
		Lifetime:     m.cfg.InstanceLifetime,
	}
	inst, err := NewInstance(m.log, instCfg)
	if err != nil {
		return nil, err
	}
	m.insts[fnName] = inst
	managerInstanceCreationsTotal.WithLabelValues(m.cfg.PodName, fnName).Inc()

	go func() {
		instCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m.log.Info("instance goroutine started",
			zap.String("pod", m.cfg.PodName),
			zap.String("function", fnName))
		if err := inst.Run(instCtx); err != nil {
			m.log.Error("instance run failed",
				zap.String("pod", m.cfg.PodName),
				zap.String("function_name", fnName),
				zap.Error(err))
		}
		m.instMu.Lock()
		delete(m.insts, fnName)
		managerInstanceDeletionsTotal.WithLabelValues(m.cfg.PodName, fnName).Inc()
		m.instMu.Unlock()
		m.log.Info("instance removed",
			zap.String("pod", m.cfg.PodName),
			zap.String("function_name", fnName))
	}()

	return inst, nil
}

func (m *Manager) runHintsConsumer(ctx context.Context) error {
	m.log.Info("hints consumer initializing", zap.String("pod", m.cfg.PodName))
	cons, err := m.ensureConsumer(ctx, "mgr-hints", "task.hints",
		m.cfg.AckWait, 1, m.cfg.MaxDeliver, m.cfg.Backoff)
	if err != nil {
		return err
	}
	m.log.Info("hints consumer ready", zap.String("pod", m.cfg.PodName))

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fetchAttempts := 0
	for {
		select {
		case <-ctx.Done():
			m.log.Info("hints consumer cancelled", zap.String("pod", m.cfg.PodName))
			return ctx.Err()
		case <-ticker.C:
			fetchAttempts++
			msg, err := fetchOne(ctx, cons)
			if err != nil {
				if errors.Is(err, jetstream.ErrNoMessages) {
					if fetchAttempts%100 == 0 {
						m.log.Debug("hints: no messages (polling)",
							zap.String("pod", m.cfg.PodName),
							zap.Int("attempts", fetchAttempts))
					}
					continue
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				m.log.Warn("hints fetch failed",
					zap.String("pod", m.cfg.PodName),
					zap.Error(err))
				continue
			}
			fetchAttempts = 0

			fnName := strings.TrimPrefix(string(msg.Data()), "task.")
			m.log.Info("hint received → starting consumer",
				zap.String("pod", m.cfg.PodName),
				zap.String("function_name", fnName),
				zap.ByteString("hint_raw", msg.Data()))

			go m.consumeFunctionQueue(ctx, fnName)
			if err := msg.Ack(); err != nil {
				m.log.Warn("hints ack failed",
					zap.String("pod", m.cfg.PodName),
					zap.Error(err))
			}
		}
	}
}

func (m *Manager) consumeFunctionQueue(ctx context.Context, fnName string) {
	m.log.Info("consumeFunctionQueue started",
		zap.String("pod", m.cfg.PodName),
		zap.String("fn", fnName))
	inst, err := m.getOrCreateInstance(fnName)
	if err != nil {
		m.log.Error("cannot create instance",
			zap.String("pod", m.cfg.PodName),
			zap.Error(err))
		return
	}

	cons, err := m.ensureConsumer(ctx, fmt.Sprintf("mgr-%s", fnName),
		fmt.Sprintf("task.%s", fnName),
		m.cfg.AckWait, m.cfg.MaxAckPending, m.cfg.MaxDeliver, m.cfg.Backoff)
	if err != nil {
		m.log.Error("create function consumer failed",
			zap.String("pod", m.cfg.PodName),
			zap.String("function_name", fnName),
			zap.Error(err))
		return
	}
	m.log.Info("task consumer ready",
		zap.String("pod", m.cfg.PodName),
		zap.String("fn", fnName))

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	pollCount := 0
	for {
		select {
		case <-ctx.Done():
			m.log.Info("consumer cancelled",
				zap.String("pod", m.cfg.PodName),
				zap.String("fn", fnName))
			return
		case <-inst.done:
			m.log.Info("instance done → stopping consumer",
				zap.String("pod", m.cfg.PodName),
				zap.String("fn", fnName))
			return
		case <-ticker.C:
			pollCount++
			msg, err := fetchOne(ctx, cons)
			if err != nil {
				if errors.Is(err, jetstream.ErrNoMessages) {
					consumerPollEmptyTotal.WithLabelValues(m.cfg.PodName, "task", fnName).Inc()
					if pollCount%50 == 0 {
						m.log.Debug("polling empty queue",
							zap.String("pod", m.cfg.PodName),
							zap.String("fn", fnName),
							zap.Int("polls", pollCount))
					}
					continue
				}
				m.log.Warn("task fetch failed",
					zap.String("pod", m.cfg.PodName),
					zap.String("function_name", fnName),
					zap.Error(err))
				continue
			}
			pollCount = 0
			consumerMessagesFetchedTotal.WithLabelValues(m.cfg.PodName, "task", fnName).Inc()
			m.log.Info("📥 TASK FETCHED",
				zap.String("pod", m.cfg.PodName),
				zap.String("fn", fnName),
				zap.Int("msg_size", len(msg.Data())))

			startTime := time.Now()
			var task Task
			if err := json.Unmarshal(msg.Data(), &task); err != nil {
				consumerMessagesFailedTotal.WithLabelValues(m.cfg.PodName, fnName, "unmarshal").Inc()
				m.log.Error("❌ unmarshal task failed",
					zap.String("pod", m.cfg.PodName),
					zap.ByteString("raw", msg.Data()),
					zap.Error(err))
				msg.Nak()
				continue
			}

			if task.FunctionID != fnName {
				consumerMessagesFailedTotal.WithLabelValues(m.cfg.PodName, fnName, "mismatch").Inc()
				m.log.Warn("❌ function mismatch",
					zap.String("pod", m.cfg.PodName),
					zap.String("expected", fnName),
					zap.String("got", task.FunctionID),
					zap.ByteString("payload", msg.Data()))
				msg.Ack()
				continue
			}

			m.log.Info("✅ task received",
				zap.String("pod", m.cfg.PodName),
				zap.String("task_id", task.TaskID),
				zap.Duration("execution_time", task.ExecutionTime),
				zap.Int("payload_size", len(msg.Data())))

			taskPayloadSizeBytes.WithLabelValues(m.cfg.PodName, fnName).Observe(float64(len(msg.Data())))

			if err := inst.Execute(ctx, &task); err != nil {
				consumerMessagesFailedTotal.WithLabelValues(m.cfg.PodName, fnName, "execute").Inc()
				m.log.Error("💥 task execute failed",
					zap.String("pod", m.cfg.PodName),
					zap.String("task_id", task.TaskID),
					zap.Error(err))
				continue
			}

			executionDuration := time.Since(startTime)
			taskExecutionDuration.WithLabelValues(m.cfg.PodName, fnName).Observe(executionDuration.Seconds())
			consumerMessagesProcessedTotal.WithLabelValues(m.cfg.PodName, fnName).Inc()

			if err := msg.Ack(); err != nil {
				m.log.Error("❌ task ack failed",
					zap.String("pod", m.cfg.PodName),
					zap.String("task_id", task.TaskID),
					zap.Error(err))
				continue
			}
			m.log.Info("🎉 task COMPLETED + ACKED",
				zap.String("pod", m.cfg.PodName),
				zap.String("task_id", task.TaskID),
				zap.String("function", fnName),
				zap.Duration("duration", executionDuration))
		}
	}
}

func (m *Manager) ensureConsumer(
	ctx context.Context,
	baseDurable, filterSubject string,
	ackWait time.Duration,
	maxAckPending, maxDeliver int,
	backoff []time.Duration,
) (jetstream.Consumer, error) {

	durable := fmt.Sprintf("%s-%s", m.cfg.PodName, baseDurable)
	m.log.Debug("ensureConsumer",
		zap.String("pod", m.cfg.PodName),
		zap.String("durable", durable),
		zap.String("subject", filterSubject))

	cons, err := m.us.TaskStream.Consumer(ctx, durable)
	if err == nil {
		m.log.Info("consumer exists", zap.String("durable", durable))
		return cons, nil
	}
	m.log.Info("creating new consumer", zap.String("durable", durable))

	cfg := jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxAckPending: maxAckPending,
		MaxDeliver:    maxDeliver,
		BackOff:       backoff,
	}
	cons, err = m.us.TaskStream.CreateConsumer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", durable, err)
	}
	m.log.Info("consumer created", zap.String("durable", durable))
	return cons, nil
}

func fetchOne(ctx context.Context, cons jetstream.Consumer) (jetstream.Msg, error) {
	msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(100*time.Millisecond))
	if err != nil {
		return nil, err
	}

	for msg := range msgs.Messages() {
		return msg, nil
	}
	return nil, jetstream.ErrNoMessages
}
