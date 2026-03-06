package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
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

func main() {
	cfg := parseFlags()

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

	taskType := fmt.Sprintf("%s-%d", cfg.taskTypePrefix, time.Now().UTC().UnixNano())
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
		completed       = make(map[string]cloudclaw.TaskResult, total)
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
					completed[item.TaskID] = item
					usageTotal := 0
					if item.Usage != nil {
						usageTotal = item.Usage.TotalTokens
					}
					log.Printf("result received: task_id=%s user=%s status=%s usage_total=%d output_len=%d", item.TaskID, job.userID, item.Status, usageTotal, len(item.Output))
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
				mu.Unlock()
				log.Printf("submitted: task_id=%s user=%s idx=%d", t.ID, job.userID, job.index)
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
	completedCount := len(completed)
	failedCount := 0
	canceledCount := 0
	for _, item := range completed {
		switch item.Status {
		case string("FAILED"):
			failedCount++
		case string("CANCELED"):
			canceledCount++
		}
	}
	foreign := foreignConsumed
	mu.Unlock()

	log.Printf("simulation finished: submitted=%d completed=%d failed=%d canceled=%d submit_errors=%d foreign_consumed=%d", submittedCount, completedCount, failedCount, canceledCount, submitErrCount, foreign)

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
	inputPrefix    string
}

func parseFlags() config {
	var usersCSV string
	cfg := config{}
	flag.StringVar(&cfg.dataDir, "data-dir", "./cloudclaw_data/data", "cloudclaw data directory")
	flag.StringVar(&cfg.dbDriver, "db-driver", "sqlite", "database driver: sqlite|postgres")
	flag.StringVar(&cfg.dbDSN, "db-dsn", "", "database dsn")
	flag.StringVar(&usersCSV, "users", "sim_u1,sim_u2", "comma-separated user ids")
	flag.IntVar(&cfg.tasksPerUser, "tasks-per-user", 5, "number of tasks per user")
	flag.IntVar(&cfg.submitWorkers, "submit-workers", 4, "number of concurrent submit workers")
	flag.IntVar(&cfg.dequeueLimit, "dequeue-limit", 20, "max results to dequeue per poll")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 1*time.Second, "result queue polling interval")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Minute, "overall simulation timeout")
	flag.IntVar(&cfg.maxRetries, "max-retries", 2, "task max retries")
	flag.StringVar(&cfg.taskTypePrefix, "task-type-prefix", "sim", "task_type prefix for this simulation run")
	flag.StringVar(&cfg.inputPrefix, "input-prefix", "simulation task", "task input prefix")
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
