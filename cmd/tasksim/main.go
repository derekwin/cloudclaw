package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cloudclaw/pkg/cloudclaw"
)

type submitJob struct {
	userID string
	index  int
}

type completedRecord struct {
	result     cloudclaw.TaskResult
	dequeuedAt time.Time
}

type scalarStats struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

type recoveryStats struct {
	RequeueEvents         int         `json:"requeue_events"`
	RecoveredEvents       int         `json:"recovered_events"`
	RecoveryLatencyMS     scalarStats `json:"recovery_latency_ms"`
	RecoveredTaskTotal    int         `json:"recovered_task_total"`
	RecoveredTaskSuccess  int         `json:"recovered_task_success"`
	RecoveredTaskRate     float64     `json:"recovered_task_rate"`
	RecoveredTaskSuccRate float64     `json:"recovered_task_success_rate"`
}

type simulationSummary struct {
	TaskType          string        `json:"task_type"`
	StartedAt         time.Time     `json:"started_at"`
	FinishedAt        time.Time     `json:"finished_at"`
	DurationMS        int64         `json:"duration_ms"`
	Submitted         int           `json:"submitted"`
	Completed         int           `json:"completed"`
	Succeeded         int           `json:"succeeded"`
	Failed            int           `json:"failed"`
	Canceled          int           `json:"canceled"`
	SubmitErrors      int           `json:"submit_errors"`
	ForeignConsumed   int           `json:"foreign_consumed"`
	ThroughputTPS     float64       `json:"throughput_tps"`
	EndToEndLatencyMS scalarStats   `json:"end_to_end_latency_ms"`
	DeliveryLagMS     scalarStats   `json:"delivery_lag_ms"`
	QueueLatencyMS    scalarStats   `json:"queue_latency_ms"`
	RunLatencyMS      scalarStats   `json:"run_latency_ms"`
	AttemptCount      scalarStats   `json:"attempt_count"`
	RetriesObserved   int           `json:"retries_observed"`
	EventRecovery     recoveryStats `json:"event_recovery"`
	GeneratedAt       time.Time     `json:"generated_at"`
}

func main() {
	cfg := parseFlags()
	runStart := time.Now().UTC()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if cfg.timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, cfg.timeout)
		defer timeoutCancel()
	}

	cli, err := cloudclaw.NewClient(cloudclaw.Config{
		DataDir:  cfg.dataDir,
		DBDriver: cfg.dbDriver,
		DBDSN:    cfg.dbDSN,
	})
	if err != nil {
		log.Fatalf("create client failed: %v", err)
	}
	defer cli.Close()

	taskType := strings.TrimSpace(cfg.taskType)
	if taskType == "" {
		taskType = fmt.Sprintf("%s-%d", cfg.taskTypePrefix, time.Now().UTC().UnixNano())
	}
	jobs := buildJobs(cfg.users, cfg.tasksPerUser)
	total := len(jobs)
	if total == 0 {
		log.Fatal("no jobs to submit")
	}

	log.Printf("simulation started: task_type=%s total_jobs=%d submit_workers=%d poll_interval=%s dequeue_limit=%d", taskType, total, cfg.submitWorkers, cfg.pollInterval, cfg.dequeueLimit)
	log.Printf("warning: dequeue API is global; run this in isolated test environment")

	var (
		mu              sync.Mutex
		submitted       = make(map[string]submitJob, total)
		submittedAt     = make(map[string]time.Time, total)
		completed       = make(map[string]completedRecord, total)
		submitErrs      []error
		foreignConsumed int
	)

	jobsCh := make(chan submitJob)
	resultDone := make(chan struct{})
	allDone := make(chan struct{})

	go func() {
		defer close(resultDone)
		ticker := time.NewTicker(cfg.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				items, err := cli.DequeueTaskResults(cfg.dequeueLimit)
				if err != nil {
					log.Printf("dequeue results failed: %v", err)
					continue
				}
				if len(items) == 0 {
					continue
				}

				mu.Lock()
				for _, item := range items {
					job, ok := submitted[item.TaskID]
					if !ok {
						foreignConsumed++
						continue
					}
					if _, exists := completed[item.TaskID]; exists {
						continue
					}
					completed[item.TaskID] = completedRecord{
						result:     item,
						dequeuedAt: time.Now().UTC(),
					}
					usageTotal := 0
					if item.Usage != nil {
						usageTotal = item.Usage.TotalTokens
					}
					if cfg.verbose {
						log.Printf("result received: task_id=%s user=%s status=%s container=%s usage_total=%d output_len=%d", item.TaskID, job.userID, item.Status, item.ContainerID, usageTotal, len(item.Output))
					}
				}
				if len(completed) >= total {
					mu.Unlock()
					close(allDone)
					return
				}
				mu.Unlock()
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < cfg.submitWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobsCh {
				input := fmt.Sprintf("%s | worker=%d user=%s idx=%d", cfg.inputPrefix, workerID, job.userID, job.index)
				t, err := cli.SubmitTask(cloudclaw.SubmitTaskRequest{
					UserID:     job.userID,
					TaskType:   taskType,
					Input:      input,
					MaxRetries: cfg.maxRetries,
				})
				mu.Lock()
				if err != nil {
					submitErrs = append(submitErrs, fmt.Errorf("user=%s idx=%d: %w", job.userID, job.index, err))
					mu.Unlock()
					continue
				}
				submitted[t.ID] = job
				submittedAt[t.ID] = time.Now().UTC()
				mu.Unlock()
				if cfg.verbose {
					log.Printf("submitted: task_id=%s user=%s idx=%d", t.ID, job.userID, job.index)
				}
			}
		}(i + 1)
	}

	go func() {
		defer close(jobsCh)
		for _, job := range jobs {
			select {
			case <-ctx.Done():
				return
			case jobsCh <- job:
			}
		}
	}()

	wg.Wait()

	mu.Lock()
	submitErrCount := len(submitErrs)
	submittedCount := len(submitted)
	mu.Unlock()
	if submitErrCount > 0 {
		log.Printf("submit errors: %d", submitErrCount)
		for _, err := range submitErrs {
			log.Printf("submit error: %v", err)
		}
	}
	if submittedCount == 0 {
		cancel()
		<-resultDone
		log.Fatal("all submits failed")
	}

	select {
	case <-ctx.Done():
		log.Printf("context done before all results: %v", ctx.Err())
	case <-allDone:
	}

	cancel()
	<-resultDone

	mu.Lock()
	completedSnapshot := make(map[string]completedRecord, len(completed))
	for k, v := range completed {
		completedSnapshot[k] = v
	}
	submittedAtSnapshot := make(map[string]time.Time, len(submittedAt))
	for k, v := range submittedAt {
		submittedAtSnapshot[k] = v
	}
	completedCount := len(completed)
	failedCount := 0
	canceledCount := 0
	for _, item := range completed {
		switch item.result.Status {
		case string("FAILED"):
			failedCount++
		case string("CANCELED"):
			canceledCount++
		}
	}
	foreign := foreignConsumed
	mu.Unlock()

	log.Printf("simulation finished: submitted=%d completed=%d failed=%d canceled=%d submit_errors=%d foreign_consumed=%d", submittedCount, completedCount, failedCount, canceledCount, submitErrCount, foreign)

	runEnd := time.Now().UTC()
	summary := buildSummary(cfg, taskType, runStart, runEnd, submittedCount, submitErrCount, foreign, completedSnapshot, submittedAtSnapshot)
	if cfg.fetchFinalTask || cfg.collectEvents {
		enrichSummary(context.Background(), cli, cfg, completedSnapshot, &summary)
	}
	if cfg.summaryFile != "" {
		if err := writeSummaryFile(cfg.summaryFile, summary); err != nil {
			log.Printf("write summary file failed: %v", err)
		} else {
			log.Printf("summary written: %s", cfg.summaryFile)
		}
	}
	if cfg.appendCSV != "" {
		if err := appendSummaryCSV(cfg.appendCSV, summary); err != nil {
			log.Printf("append summary csv failed: %v", err)
		} else {
			log.Printf("summary appended: %s", cfg.appendCSV)
		}
	}
	log.Printf("summary: task_type=%s throughput_tps=%.4f e2e_p95_ms=%.2f e2e_p99_ms=%.2f recovered_tasks=%d recovered_task_success=%d",
		summary.TaskType,
		summary.ThroughputTPS,
		summary.EndToEndLatencyMS.P95,
		summary.EndToEndLatencyMS.P99,
		summary.EventRecovery.RecoveredTaskTotal,
		summary.EventRecovery.RecoveredTaskSuccess,
	)

	if completedCount < submittedCount {
		os.Exit(1)
	}
}

type config struct {
	dataDir        string
	dbDriver       string
	dbDSN          string
	users          []string
	tasksPerUser   int
	submitWorkers  int
	dequeueLimit   int
	pollInterval   time.Duration
	timeout        time.Duration
	maxRetries     int
	taskTypePrefix string
	taskType       string
	inputPrefix    string
	summaryFile    string
	appendCSV      string
	fetchFinalTask bool
	collectEvents  bool
	verbose        bool
}

func parseFlags() config {
	var usersCSV string
	cfg := config{}
	flag.StringVar(&cfg.dataDir, "data-dir", "./cloudclaw_data/data", "cloudclaw data directory")
	flag.StringVar(&cfg.dbDriver, "db-driver", "postgres", "database driver: postgres")
	flag.StringVar(&cfg.dbDSN, "db-dsn", "", "database dsn (required): postgres://user:pass@host:port/db?sslmode=disable")
	flag.StringVar(&usersCSV, "users", "sim_u1,sim_u2", "comma-separated user ids")
	flag.IntVar(&cfg.tasksPerUser, "tasks-per-user", 5, "number of tasks per user")
	flag.IntVar(&cfg.submitWorkers, "submit-workers", 4, "number of concurrent submit workers")
	flag.IntVar(&cfg.dequeueLimit, "dequeue-limit", 20, "max results to dequeue per poll")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 1*time.Second, "result queue polling interval")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Minute, "overall simulation timeout")
	flag.IntVar(&cfg.maxRetries, "max-retries", 2, "task max retries")
	flag.StringVar(&cfg.taskTypePrefix, "task-type-prefix", "sim", "task_type prefix for this simulation run")
	flag.StringVar(&cfg.taskType, "task-type", "", "fixed task_type for this simulation run (optional)")
	flag.StringVar(&cfg.inputPrefix, "input-prefix", "simulation task", "task input prefix")
	flag.StringVar(&cfg.summaryFile, "summary-file", "", "write structured summary JSON to this path")
	flag.StringVar(&cfg.appendCSV, "append-csv", "", "append one-line summary CSV row to this path")
	flag.BoolVar(&cfg.fetchFinalTask, "fetch-final-task", true, "fetch final task state to compute queue/run latency and attempts")
	flag.BoolVar(&cfg.collectEvents, "collect-events", false, "fetch task events to compute recovery metrics (slower)")
	flag.BoolVar(&cfg.verbose, "verbose", true, "print per-task submit/result logs")
	flag.Parse()

	cfg.users = parseUsers(usersCSV)
	if len(cfg.users) == 0 {
		log.Fatal("users cannot be empty")
	}
	if cfg.tasksPerUser <= 0 {
		log.Fatal("tasks-per-user must be > 0")
	}
	if cfg.submitWorkers <= 0 {
		log.Fatal("submit-workers must be > 0")
	}
	if cfg.dequeueLimit <= 0 {
		log.Fatal("dequeue-limit must be > 0")
	}
	if cfg.pollInterval <= 0 {
		log.Fatal("poll-interval must be > 0")
	}
	cfg.dbDriver = strings.ToLower(strings.TrimSpace(cfg.dbDriver))
	if cfg.dbDriver == "" {
		cfg.dbDriver = "postgres"
	}
	if cfg.dbDriver == "postgresql" {
		cfg.dbDriver = "postgres"
	}
	if cfg.dbDriver != "postgres" {
		log.Fatalf("unsupported db-driver=%q: only postgres is supported", cfg.dbDriver)
	}
	if strings.TrimSpace(cfg.dbDSN) == "" {
		log.Fatal("db-dsn is required when db-driver=postgres")
	}
	if strings.TrimSpace(cfg.taskTypePrefix) == "" {
		log.Fatal("task-type-prefix cannot be empty")
	}
	return cfg
}

func parseUsers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

func buildJobs(users []string, tasksPerUser int) []submitJob {
	jobs := make([]submitJob, 0, len(users)*tasksPerUser)
	for _, user := range users {
		for i := 1; i <= tasksPerUser; i++ {
			jobs = append(jobs, submitJob{userID: user, index: i})
		}
	}
	return jobs
}

func buildSummary(cfg config, taskType string, runStart, runEnd time.Time, submittedCount, submitErrCount, foreign int, completed map[string]completedRecord, submittedAt map[string]time.Time) simulationSummary {
	e2eMS := make([]float64, 0, len(completed))
	deliveryLagMS := make([]float64, 0, len(completed))

	succeeded := 0
	failed := 0
	canceled := 0
	for taskID, item := range completed {
		switch item.result.Status {
		case string("SUCCEEDED"):
			succeeded++
		case string("FAILED"):
			failed++
		case string("CANCELED"):
			canceled++
		}

		if submitAt, ok := submittedAt[taskID]; ok {
			resultAt := item.result.CreatedAt
			if resultAt.IsZero() {
				resultAt = item.dequeuedAt
			}
			if !resultAt.Before(submitAt) {
				e2eMS = append(e2eMS, float64(resultAt.Sub(submitAt).Milliseconds()))
			}
		}
		if !item.result.CreatedAt.IsZero() && !item.dequeuedAt.Before(item.result.CreatedAt) {
			deliveryLagMS = append(deliveryLagMS, float64(item.dequeuedAt.Sub(item.result.CreatedAt).Milliseconds()))
		}
	}

	durationMS := runEnd.Sub(runStart).Milliseconds()
	throughput := 0.0
	if durationMS > 0 {
		throughput = float64(len(completed)) / (float64(durationMS) / 1000.0)
	}

	return simulationSummary{
		TaskType:          taskType,
		StartedAt:         runStart,
		FinishedAt:        runEnd,
		DurationMS:        durationMS,
		Submitted:         submittedCount,
		Completed:         len(completed),
		Succeeded:         succeeded,
		Failed:            failed,
		Canceled:          canceled,
		SubmitErrors:      submitErrCount,
		ForeignConsumed:   foreign,
		ThroughputTPS:     throughput,
		EndToEndLatencyMS: calcStats(e2eMS),
		DeliveryLagMS:     calcStats(deliveryLagMS),
		GeneratedAt:       time.Now().UTC(),
	}
}

func enrichSummary(ctx context.Context, cli *cloudclaw.Client, cfg config, completed map[string]completedRecord, summary *simulationSummary) {
	queueMS := make([]float64, 0, len(completed))
	runMS := make([]float64, 0, len(completed))
	attempts := make([]float64, 0, len(completed))
	retries := 0

	requeueEvents := 0
	recoveredEvents := 0
	recoveryMS := []float64{}
	recoveredTasks := 0
	recoveredTasksSuccess := 0

	for taskID, item := range completed {
		if err := ctx.Err(); err != nil {
			break
		}
		task, err := cli.GetTask(taskID)
		if err != nil {
			log.Printf("fetch task failed: task_id=%s err=%v", taskID, err)
			continue
		}
		attempts = append(attempts, float64(task.Attempts))
		if task.Attempts > 1 {
			retries++
		}
		if task.StartedAt != nil && !task.StartedAt.IsZero() && !task.StartedAt.Before(task.EnqueuedAt) {
			queueMS = append(queueMS, float64(task.StartedAt.Sub(task.EnqueuedAt).Milliseconds()))
		}
		if task.StartedAt != nil && task.FinishedAt != nil && !task.FinishedAt.Before(*task.StartedAt) {
			runMS = append(runMS, float64(task.FinishedAt.Sub(*task.StartedAt).Milliseconds()))
		}

		if !cfg.collectEvents {
			continue
		}

		events, err := cli.TaskEvents(taskID)
		if err != nil {
			log.Printf("fetch task events failed: task_id=%s err=%v", taskID, err)
			continue
		}
		taskRequeued := false
		taskRecovered := false
		for i, evt := range events {
			if evt.ToStatus != string("QUEUED") {
				continue
			}
			if !isRecoveryRequeueReason(evt.Reason) {
				continue
			}
			requeueEvents++
			taskRequeued = true
			for j := i + 1; j < len(events); j++ {
				if events[j].ToStatus != string("RUNNING") {
					continue
				}
				if events[j].At.Before(evt.At) {
					continue
				}
				latency := events[j].At.Sub(evt.At).Milliseconds()
				if latency >= 0 {
					recoveredEvents++
					taskRecovered = true
					recoveryMS = append(recoveryMS, float64(latency))
				}
				break
			}
		}

		if taskRequeued && taskRecovered {
			recoveredTasks++
			if item.result.Status == string("SUCCEEDED") {
				recoveredTasksSuccess++
			}
		}
	}

	summary.QueueLatencyMS = calcStats(queueMS)
	summary.RunLatencyMS = calcStats(runMS)
	summary.AttemptCount = calcStats(attempts)
	summary.RetriesObserved = retries
	summary.EventRecovery.RequeueEvents = requeueEvents
	summary.EventRecovery.RecoveredEvents = recoveredEvents
	summary.EventRecovery.RecoveryLatencyMS = calcStats(recoveryMS)
	summary.EventRecovery.RecoveredTaskTotal = recoveredTasks
	summary.EventRecovery.RecoveredTaskSuccess = recoveredTasksSuccess
	if requeueEvents > 0 {
		summary.EventRecovery.RecoveredTaskRate = float64(recoveredEvents) / float64(requeueEvents)
	}
	if recoveredTasks > 0 {
		summary.EventRecovery.RecoveredTaskSuccRate = float64(recoveredTasksSuccess) / float64(recoveredTasks)
	}
}

func isRecoveryRequeueReason(reason string) bool {
	reason = strings.TrimSpace(strings.ToLower(reason))
	return reason == "lease expired" || strings.HasPrefix(reason, "retry scheduled:")
}

func calcStats(samples []float64) scalarStats {
	if len(samples) == 0 {
		return scalarStats{}
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)

	sum := 0.0
	for _, v := range sorted {
		sum += v
	}

	return scalarStats{
		Count: len(sorted),
		Min:   sorted[0],
		Max:   sorted[len(sorted)-1],
		Mean:  round2(sum / float64(len(sorted))),
		P50:   round2(percentile(sorted, 50)),
		P90:   round2(percentile(sorted, 90)),
		P95:   round2(percentile(sorted, 95)),
		P99:   round2(percentile(sorted, 99)),
	}
}

func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[n-1]
	}
	pos := (p / 100.0) * float64(n-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return sorted[lower]
	}
	weight := pos - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func writeSummaryFile(path string, summary simulationSummary) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func appendSummaryCSV(path string, summary simulationSummary) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	needHeader := false
	if st, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			needHeader = true
		} else {
			return err
		}
	} else if st.Size() == 0 {
		needHeader = true
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if needHeader {
		if err := w.Write([]string{
			"task_type", "started_at", "finished_at", "duration_ms",
			"submitted", "completed", "succeeded", "failed", "canceled", "submit_errors", "foreign_consumed",
			"throughput_tps", "e2e_p50_ms", "e2e_p95_ms", "e2e_p99_ms", "e2e_max_ms",
			"queue_p95_ms", "run_p95_ms", "attempt_p95", "retries_observed",
			"requeue_events", "recovered_events", "recovered_task_total", "recovered_task_success", "recovered_task_success_rate",
		}); err != nil {
			return err
		}
	}

	row := []string{
		summary.TaskType,
		summary.StartedAt.Format(time.RFC3339Nano),
		summary.FinishedAt.Format(time.RFC3339Nano),
		strconv.FormatInt(summary.DurationMS, 10),
		strconv.Itoa(summary.Submitted),
		strconv.Itoa(summary.Completed),
		strconv.Itoa(summary.Succeeded),
		strconv.Itoa(summary.Failed),
		strconv.Itoa(summary.Canceled),
		strconv.Itoa(summary.SubmitErrors),
		strconv.Itoa(summary.ForeignConsumed),
		fmt.Sprintf("%.4f", summary.ThroughputTPS),
		fmt.Sprintf("%.2f", summary.EndToEndLatencyMS.P50),
		fmt.Sprintf("%.2f", summary.EndToEndLatencyMS.P95),
		fmt.Sprintf("%.2f", summary.EndToEndLatencyMS.P99),
		fmt.Sprintf("%.2f", summary.EndToEndLatencyMS.Max),
		fmt.Sprintf("%.2f", summary.QueueLatencyMS.P95),
		fmt.Sprintf("%.2f", summary.RunLatencyMS.P95),
		fmt.Sprintf("%.2f", summary.AttemptCount.P95),
		strconv.Itoa(summary.RetriesObserved),
		strconv.Itoa(summary.EventRecovery.RequeueEvents),
		strconv.Itoa(summary.EventRecovery.RecoveredEvents),
		strconv.Itoa(summary.EventRecovery.RecoveredTaskTotal),
		strconv.Itoa(summary.EventRecovery.RecoveredTaskSuccess),
		fmt.Sprintf("%.4f", summary.EventRecovery.RecoveredTaskSuccRate),
	}
	if err := w.Write(row); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}
