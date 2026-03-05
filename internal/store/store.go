package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"cloudclaw/internal/fsutil"
	"cloudclaw/internal/model"
)

type SubmitTaskInput struct {
	UserID     string
	TaskType   string
	Input      string
	MaxRetries int
}

type Config struct {
	BaseDir string
	Driver  string // sqlite | postgres
	DSN     string
}

type Store struct {
	baseDir string
	driver  string
	sqlDrv  string
	dsn     string
	db      *sql.DB
	dialect dialect
}

var idSeq atomic.Uint64

type dialect interface {
	placeholder(i int) string
	boolValue(v bool) any
}

type sqliteDialect struct{}

type postgresDialect struct{}

func (sqliteDialect) placeholder(i int) string { return "?" }
func (sqliteDialect) boolValue(v bool) any {
	if v {
		return 1
	}
	return 0
}
func (postgresDialect) placeholder(i int) string { return fmt.Sprintf("$%d", i) }
func (postgresDialect) boolValue(v bool) any     { return v }

func New(baseDir string) (*Store, error) {
	return NewWithConfig(Config{BaseDir: baseDir, Driver: "sqlite"})
}

func NewWithConfig(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.BaseDir) == "" {
		return nil, errors.New("base dir is required")
	}
	if strings.TrimSpace(cfg.Driver) == "" {
		cfg.Driver = "sqlite"
	}
	cfg.Driver = strings.ToLower(strings.TrimSpace(cfg.Driver))

	if err := fsutil.EnsureDir(cfg.BaseDir); err != nil {
		return nil, err
	}
	for _, p := range []string{
		filepath.Join(cfg.BaseDir, "users"),
		filepath.Join(cfg.BaseDir, "runs"),
		filepath.Join(cfg.BaseDir, "snapshots"),
	} {
		if err := fsutil.EnsureDir(p); err != nil {
			return nil, err
		}
	}

	var d dialect
	sqlDriverName := cfg.Driver
	dsn := strings.TrimSpace(cfg.DSN)
	switch cfg.Driver {
	case "sqlite":
		d = sqliteDialect{}
		sqlDriverName = "sqlite3"
		if dsn == "" {
			dbPath := filepath.Join(cfg.BaseDir, "cloudclaw.db")
			dsn = fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=1&_loc=UTC", dbPath)
		}
	case "postgres", "postgresql":
		d = postgresDialect{}
		cfg.Driver = "postgres"
		if dsn == "" {
			return nil, errors.New("postgres dsn is required")
		}
	default:
		return nil, fmt.Errorf("unsupported driver: %s", cfg.Driver)
	}

	db, err := sql.Open(sqlDriverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(10 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{
		baseDir: cfg.BaseDir,
		driver:  cfg.Driver,
		sqlDrv:  sqlDriverName,
		dsn:     dsn,
		db:      db,
		dialect: d,
	}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) UserDataDir(userID string) string {
	return filepath.Join(s.baseDir, "users", userID, "data")
}

func (s *Store) UserSnapshotBaseDir(userID string) string {
	return filepath.Join(s.baseDir, "snapshots", userID)
}

func (s *Store) RunDir(taskID string, attempt int) string {
	return filepath.Join(s.baseDir, "runs", fmt.Sprintf("%s-attempt-%d", taskID, attempt))
}

func (s *Store) SubmitTask(in SubmitTaskInput) (model.Task, error) {
	userID := strings.TrimSpace(in.UserID)
	if userID == "" {
		return model.Task{}, errors.New("user id is required")
	}
	taskType := strings.TrimSpace(in.TaskType)
	if taskType == "" {
		return model.Task{}, errors.New("task type is required")
	}

	now := time.Now().UTC()
	task := model.Task{
		ID:         genID("tsk"),
		UserID:     userID,
		TaskType:   taskType,
		Input:      in.Input,
		Priority:   1,
		Status:     model.StatusQueued,
		Attempts:   0,
		MaxRetries: max(0, in.MaxRetries),
		CreatedAt:  now,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer rollback(tx)

	insertTask := fmt.Sprintf(`
INSERT INTO tasks (
  id, user_id, task_type, input, priority, status, attempts, max_retries,
  container_id, lease_until, error_message, cancel_requested,
  created_at, enqueued_at, updated_at, started_at, finished_at, last_heartbeat_at,
  usage_prompt_tokens, usage_completion_tokens, usage_total_tokens
) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
`,
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9), s.ph(10), s.ph(11), s.ph(12), s.ph(13), s.ph(14), s.ph(15), s.ph(16), s.ph(17), s.ph(18), s.ph(19), s.ph(20), s.ph(21),
	)

	if _, err := tx.Exec(insertTask,
		task.ID, task.UserID, task.TaskType, task.Input, task.Priority, string(task.Status), task.Attempts, task.MaxRetries,
		"", nil, "", s.dialect.boolValue(false),
		task.CreatedAt, task.EnqueuedAt, task.UpdatedAt, nil, nil, nil,
		nil, nil, nil,
	); err != nil {
		return model.Task{}, err
	}

	if err := s.insertEventTx(tx, model.TaskEvent{
		ID:         genID("evt"),
		TaskID:     task.ID,
		FromStatus: "",
		ToStatus:   string(model.StatusQueued),
		Reason:     "task submitted",
		At:         now,
	}); err != nil {
		return model.Task{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return task, nil
}

func (s *Store) GetTask(taskID string) (model.Task, error) {
	row := s.db.QueryRow(s.selectTaskByIDSQL(), taskID)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, fmt.Errorf("task %s not found", taskID)
		}
		return model.Task{}, err
	}
	return task, nil
}

func (s *Store) QueueLength() (int, error) {
	row := s.db.QueryRow("SELECT COUNT(1) FROM tasks WHERE status = ?", string(model.StatusQueued))
	if s.driver == "postgres" {
		row = s.db.QueryRow("SELECT COUNT(1) FROM tasks WHERE status = $1", string(model.StatusQueued))
	}
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) CancelTask(taskID string) (model.Task, error) {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer rollback(tx)

	task, err := s.getTaskTx(tx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, fmt.Errorf("task %s not found", taskID)
		}
		return model.Task{}, err
	}

	switch task.Status {
	case model.StatusQueued:
		if _, err := tx.Exec(s.updateTaskStatusSQL(), string(model.StatusCanceled), "canceled by user", now, now, taskID); err != nil {
			return model.Task{}, err
		}
		if err := s.insertEventTx(tx, model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     taskID,
			FromStatus: string(model.StatusQueued),
			ToStatus:   string(model.StatusCanceled),
			Reason:     "task canceled before start",
			At:         now,
		}); err != nil {
			return model.Task{}, err
		}
	case model.StatusRunning:
		if _, err := tx.Exec(s.updateCancelRequestedSQL(), s.dialect.boolValue(true), now, taskID); err != nil {
			return model.Task{}, err
		}
		if err := s.insertEventTx(tx, model.TaskEvent{
			ID:          genID("evt"),
			TaskID:      taskID,
			FromStatus:  string(model.StatusRunning),
			ToStatus:    string(model.StatusRunning),
			Reason:      "cancel requested",
			ContainerID: task.ContainerID,
			At:          now,
		}); err != nil {
			return model.Task{}, err
		}
	case model.StatusSuccess, model.StatusFailed, model.StatusCanceled:
		return model.Task{}, fmt.Errorf("task already terminal: %s", task.Status)
	}

	if err := tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return s.GetTask(taskID)
}

func (s *Store) DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error) {
	now := time.Now().UTC()
	leaseUntil := now.Add(leaseDuration)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	query := s.dequeueUpdateSQL()
	row := tx.QueryRow(query,
		string(model.StatusRunning),
		containerID,
		now,
		now,
		leaseUntil,
		now,
		string(model.StatusQueued),
	)

	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}

	if err := s.insertEventTx(tx, model.TaskEvent{
		ID:          genID("evt"),
		TaskID:      task.ID,
		FromStatus:  string(model.StatusQueued),
		ToStatus:    string(model.StatusRunning),
		Reason:      "assigned to container",
		ContainerID: containerID,
		At:          now,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) Heartbeat(taskID, containerID string, leaseDuration time.Duration) error {
	now := time.Now().UTC()
	leaseUntil := now.Add(leaseDuration)
	res, err := s.db.Exec(s.heartbeatSQL(), leaseUntil, now, now, taskID, string(model.StatusRunning), containerID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("task %s not running or owned by another container", taskID)
	}
	return nil
}

func (s *Store) IsCancelRequested(taskID string) (bool, error) {
	row := s.db.QueryRow(s.cancelRequestedSQL(), taskID)
	var b bool
	if err := row.Scan(&b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("task %s not found", taskID)
		}
		return false, err
	}
	return b, nil
}

func (s *Store) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollback(tx)

	res, err := tx.Exec(s.markSucceededSQL(),
		string(model.StatusSuccess), usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
		"", now, now, s.dialect.boolValue(false), taskID, string(model.StatusRunning), containerID,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("task %s not running or owned by another container", taskID)
	}

	if err := s.insertEventTx(tx, model.TaskEvent{
		ID:         genID("evt"),
		TaskID:     taskID,
		FromStatus: string(model.StatusRunning),
		ToStatus:   string(model.StatusSuccess),
		Reason:     "task completed",
		At:         now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkTaskCanceled(taskID, containerID, reason string) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollback(tx)

	res, err := tx.Exec(s.markCanceledSQL(), string(model.StatusCanceled), reason, now, now, s.dialect.boolValue(false), taskID, string(model.StatusRunning), containerID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("task %s not running or owned by another container", taskID)
	}

	if err := s.insertEventTx(tx, model.TaskEvent{
		ID:         genID("evt"),
		TaskID:     taskID,
		FromStatus: string(model.StatusRunning),
		ToStatus:   string(model.StatusCanceled),
		Reason:     reason,
		At:         now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkTaskRetryOrFail(taskID, containerID, reason string) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollback(tx)

	task, err := s.getTaskTx(tx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s not found", taskID)
		}
		return err
	}
	if task.Status != model.StatusRunning || task.ContainerID != containerID {
		return fmt.Errorf("task %s not running or owned by another container", taskID)
	}

	maxAttempts := task.MaxRetries + 1
	if task.Attempts < maxAttempts {
		if _, err := tx.Exec(s.markRetrySQL(), string(model.StatusQueued), 0, now, reason, now, s.dialect.boolValue(false), taskID); err != nil {
			return err
		}
		if err := s.insertEventTx(tx, model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     taskID,
			FromStatus: string(model.StatusRunning),
			ToStatus:   string(model.StatusQueued),
			Reason:     "retry scheduled: " + reason,
			At:         now,
		}); err != nil {
			return err
		}
		return tx.Commit()
	}

	if _, err := tx.Exec(s.markFailedSQL(), string(model.StatusFailed), reason, now, now, s.dialect.boolValue(false), taskID); err != nil {
		return err
	}
	if err := s.insertEventTx(tx, model.TaskEvent{
		ID:         genID("evt"),
		TaskID:     taskID,
		FromStatus: string(model.StatusRunning),
		ToStatus:   string(model.StatusFailed),
		Reason:     reason,
		At:         now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecoverExpiredLeases() (int, error) {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer rollback(tx)

	rows, err := tx.Query(s.selectExpiredTasksSQL(), string(model.StatusRunning), now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	taskIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		taskIDs = append(taskIDs, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range taskIDs {
		if _, err := tx.Exec(s.requeueExpiredSQL(), string(model.StatusQueued), 0, now, "lease expired, requeued", now, s.dialect.boolValue(false), id); err != nil {
			return 0, err
		}
		if err := s.insertEventTx(tx, model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     id,
			FromStatus: string(model.StatusRunning),
			ToStatus:   string(model.StatusQueued),
			Reason:     "lease expired",
			At:         now,
		}); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(taskIDs), nil
}

func (s *Store) SaveSnapshot(userID, taskID, path string) (model.Snapshot, error) {
	now := time.Now().UTC()
	snap := model.Snapshot{
		ID:        genID("snap"),
		UserID:    userID,
		TaskID:    taskID,
		Path:      path,
		CreatedAt: now,
	}

	if _, err := s.db.Exec(s.upsertSnapshotSQL(), userID, snap.ID, taskID, path, now); err != nil {
		return model.Snapshot{}, err
	}
	return snap, nil
}

func (s *Store) LatestSnapshot(userID string) (model.Snapshot, bool, error) {
	row := s.db.QueryRow(s.selectSnapshotSQL(), userID)
	var snap model.Snapshot
	if err := row.Scan(&snap.UserID, &snap.ID, &snap.TaskID, &snap.Path, &snap.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Snapshot{}, false, nil
		}
		return model.Snapshot{}, false, err
	}
	return snap, true, nil
}

func (s *Store) SetContainerStatus(info model.ContainerInfo) error {
	if _, err := s.db.Exec(s.upsertContainerSQL(), info.ID, info.State, nullableString(info.TaskID), info.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func (s *Store) ListContainers() ([]model.ContainerInfo, error) {
	rows, err := s.db.Query(s.selectContainersSQL())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ContainerInfo{}
	for rows.Next() {
		var c model.ContainerInfo
		var taskID sql.NullString
		if err := rows.Scan(&c.ID, &c.State, &taskID, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if taskID.Valid {
			c.TaskID = taskID.String
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) ListEvents(taskID string) ([]model.TaskEvent, error) {
	query := s.selectEventsSQL(false)
	args := []any{}
	if strings.TrimSpace(taskID) != "" {
		query = s.selectEventsSQL(true)
		args = append(args, taskID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.TaskEvent{}
	for rows.Next() {
		var evt model.TaskEvent
		var from, container sql.NullString
		if err := rows.Scan(&evt.ID, &evt.TaskID, &from, &evt.ToStatus, &evt.Reason, &container, &evt.At); err != nil {
			return nil, err
		}
		if from.Valid {
			evt.FromStatus = from.String
		}
		if container.Valid {
			evt.ContainerID = container.String
		}
		out = append(out, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  task_type TEXT NOT NULL,
  input TEXT NOT NULL,
  priority INTEGER NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL,
  max_retries INTEGER NOT NULL,
  container_id TEXT,
  lease_until TIMESTAMP NULL,
  error_message TEXT,
  cancel_requested BOOLEAN NOT NULL,
  created_at TIMESTAMP NOT NULL,
  enqueued_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  started_at TIMESTAMP NULL,
  finished_at TIMESTAMP NULL,
  last_heartbeat_at TIMESTAMP NULL,
  usage_prompt_tokens INTEGER NULL,
  usage_completion_tokens INTEGER NULL,
  usage_total_tokens INTEGER NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_queue ON tasks (status, priority, enqueued_at, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_lease ON tasks (status, lease_until)`,
		`CREATE TABLE IF NOT EXISTS containers (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  task_id TEXT NULL,
  updated_at TIMESTAMP NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS snapshots (
  user_id TEXT PRIMARY KEY,
  id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  path TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS task_events (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  from_status TEXT NULL,
  to_status TEXT NOT NULL,
  reason TEXT NOT NULL,
  container_id TEXT NULL,
  at TIMESTAMP NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events (task_id, at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) getTaskTx(tx *sql.Tx, taskID string) (model.Task, error) {
	row := tx.QueryRow(s.selectTaskByIDSQL(), taskID)
	return scanTask(row)
}

func (s *Store) insertEventTx(tx *sql.Tx, evt model.TaskEvent) error {
	query := fmt.Sprintf(
		"INSERT INTO task_events (id, task_id, from_status, to_status, reason, container_id, at) VALUES (%s, %s, %s, %s, %s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7),
	)
	_, err := tx.Exec(query, evt.ID, evt.TaskID, nullableString(evt.FromStatus), evt.ToStatus, evt.Reason, nullableString(evt.ContainerID), evt.At)
	return err
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (model.Task, error) {
	var t model.Task
	var containerID, errorMessage sql.NullString
	var leaseUntil, startedAt, finishedAt, heartbeatAt sql.NullTime
	var usagePrompt, usageCompletion, usageTotal sql.NullInt64
	var cancelRequested bool

	err := scanner.Scan(
		&t.ID,
		&t.UserID,
		&t.TaskType,
		&t.Input,
		&t.Priority,
		&t.Status,
		&t.Attempts,
		&t.MaxRetries,
		&containerID,
		&leaseUntil,
		&errorMessage,
		&cancelRequested,
		&t.CreatedAt,
		&t.EnqueuedAt,
		&t.UpdatedAt,
		&startedAt,
		&finishedAt,
		&heartbeatAt,
		&usagePrompt,
		&usageCompletion,
		&usageTotal,
	)
	if err != nil {
		return model.Task{}, err
	}

	if containerID.Valid {
		t.ContainerID = containerID.String
	}
	if leaseUntil.Valid {
		v := leaseUntil.Time.UTC()
		t.LeaseUntil = &v
	}
	if errorMessage.Valid {
		t.ErrorMessage = errorMessage.String
	}
	t.CancelRequested = cancelRequested
	if startedAt.Valid {
		v := startedAt.Time.UTC()
		t.StartedAt = &v
	}
	if finishedAt.Valid {
		v := finishedAt.Time.UTC()
		t.FinishedAt = &v
	}
	if heartbeatAt.Valid {
		v := heartbeatAt.Time.UTC()
		t.LastHeartbeatAt = &v
	}
	if usagePrompt.Valid || usageCompletion.Valid || usageTotal.Valid {
		t.Usage = &model.TokenUsage{}
		if usagePrompt.Valid {
			t.Usage.PromptTokens = int(usagePrompt.Int64)
		}
		if usageCompletion.Valid {
			t.Usage.CompletionTokens = int(usageCompletion.Int64)
		}
		if usageTotal.Valid {
			t.Usage.TotalTokens = int(usageTotal.Int64)
		} else {
			t.Usage.TotalTokens = t.Usage.PromptTokens + t.Usage.CompletionTokens
		}
	}
	return t, nil
}

func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func genID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UTC().UnixNano(), idSeq.Add(1))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Store) ph(i int) string {
	return s.dialect.placeholder(i)
}

func (s *Store) selectTaskByIDSQL() string {
	return `SELECT id, user_id, task_type, input, priority, status, attempts, max_retries, container_id,
lease_until, error_message, cancel_requested, created_at, enqueued_at, updated_at, started_at,
finished_at, last_heartbeat_at, usage_prompt_tokens, usage_completion_tokens, usage_total_tokens
FROM tasks WHERE id = ` + s.ph(1)
}

func (s *Store) updateTaskStatusSQL() string {
	return `UPDATE tasks SET status=` + s.ph(1) + `, error_message=` + s.ph(2) + `, updated_at=` + s.ph(3) + `, finished_at=` + s.ph(4) + ` WHERE id=` + s.ph(5)
}

func (s *Store) updateCancelRequestedSQL() string {
	return `UPDATE tasks SET cancel_requested=` + s.ph(1) + `, updated_at=` + s.ph(2) + ` WHERE id=` + s.ph(3)
}

func (s *Store) dequeueUpdateSQL() string {
	if s.driver == "postgres" {
		return `
UPDATE tasks
SET status = ` + s.ph(1) + `,
    attempts = attempts + 1,
    container_id = ` + s.ph(2) + `,
    updated_at = ` + s.ph(3) + `,
    cancel_requested = FALSE,
    started_at = COALESCE(started_at, ` + s.ph(4) + `),
    lease_until = ` + s.ph(5) + `,
    last_heartbeat_at = ` + s.ph(6) + `
WHERE id = (
    SELECT id FROM tasks
    WHERE status = ` + s.ph(7) + `
    ORDER BY priority ASC, enqueued_at ASC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, user_id, task_type, input, priority, status, attempts, max_retries, container_id,
          lease_until, error_message, cancel_requested, created_at, enqueued_at, updated_at,
          started_at, finished_at, last_heartbeat_at, usage_prompt_tokens, usage_completion_tokens,
          usage_total_tokens`
	}
	return `
UPDATE tasks
SET status = ` + s.ph(1) + `,
    attempts = attempts + 1,
    container_id = ` + s.ph(2) + `,
    updated_at = ` + s.ph(3) + `,
    cancel_requested = 0,
    started_at = COALESCE(started_at, ` + s.ph(4) + `),
    lease_until = ` + s.ph(5) + `,
    last_heartbeat_at = ` + s.ph(6) + `
WHERE id = (
    SELECT id FROM tasks
    WHERE status = ` + s.ph(7) + `
    ORDER BY priority ASC, enqueued_at ASC, created_at ASC
    LIMIT 1
)
RETURNING id, user_id, task_type, input, priority, status, attempts, max_retries, container_id,
          lease_until, error_message, cancel_requested, created_at, enqueued_at, updated_at,
          started_at, finished_at, last_heartbeat_at, usage_prompt_tokens, usage_completion_tokens,
          usage_total_tokens`
}

func (s *Store) heartbeatSQL() string {
	return `UPDATE tasks SET lease_until=` + s.ph(1) + `, last_heartbeat_at=` + s.ph(2) + `, updated_at=` + s.ph(3) + ` WHERE id=` + s.ph(4) + ` AND status=` + s.ph(5) + ` AND container_id=` + s.ph(6)
}

func (s *Store) cancelRequestedSQL() string {
	return `SELECT cancel_requested FROM tasks WHERE id=` + s.ph(1)
}

func (s *Store) markSucceededSQL() string {
	return `UPDATE tasks
SET status=` + s.ph(1) + `,
    usage_prompt_tokens=` + s.ph(2) + `,
    usage_completion_tokens=` + s.ph(3) + `,
    usage_total_tokens=` + s.ph(4) + `,
    error_message=` + s.ph(5) + `,
    updated_at=` + s.ph(6) + `,
    finished_at=` + s.ph(7) + `,
    lease_until=NULL,
    container_id=NULL,
    cancel_requested=` + s.ph(8) + `
WHERE id=` + s.ph(9) + ` AND status=` + s.ph(10) + ` AND container_id=` + s.ph(11)
}

func (s *Store) markCanceledSQL() string {
	return `UPDATE tasks
SET status=` + s.ph(1) + `,
    error_message=` + s.ph(2) + `,
    updated_at=` + s.ph(3) + `,
    finished_at=` + s.ph(4) + `,
    lease_until=NULL,
    container_id=NULL,
    cancel_requested=` + s.ph(5) + `
WHERE id=` + s.ph(6) + ` AND status=` + s.ph(7) + ` AND container_id=` + s.ph(8)
}

func (s *Store) markRetrySQL() string {
	return `UPDATE tasks
SET status=` + s.ph(1) + `,
    priority=` + s.ph(2) + `,
    enqueued_at=` + s.ph(3) + `,
    error_message=` + s.ph(4) + `,
    updated_at=` + s.ph(5) + `,
    lease_until=NULL,
    container_id=NULL,
    cancel_requested=` + s.ph(6) + `
WHERE id=` + s.ph(7)
}

func (s *Store) markFailedSQL() string {
	return `UPDATE tasks
SET status=` + s.ph(1) + `,
    error_message=` + s.ph(2) + `,
    updated_at=` + s.ph(3) + `,
    finished_at=` + s.ph(4) + `,
    lease_until=NULL,
    container_id=NULL,
    cancel_requested=` + s.ph(5) + `
WHERE id=` + s.ph(6)
}

func (s *Store) selectExpiredTasksSQL() string {
	return `SELECT id FROM tasks WHERE status=` + s.ph(1) + ` AND lease_until IS NOT NULL AND lease_until <= ` + s.ph(2)
}

func (s *Store) requeueExpiredSQL() string {
	return `UPDATE tasks
SET status=` + s.ph(1) + `,
    priority=` + s.ph(2) + `,
    enqueued_at=` + s.ph(3) + `,
    error_message=` + s.ph(4) + `,
    updated_at=` + s.ph(5) + `,
    lease_until=NULL,
    container_id=NULL,
    cancel_requested=` + s.ph(6) + `
WHERE id=` + s.ph(7)
}

func (s *Store) upsertSnapshotSQL() string {
	if s.driver == "postgres" {
		return `INSERT INTO snapshots (user_id, id, task_id, path, created_at)
VALUES (` + s.ph(1) + `, ` + s.ph(2) + `, ` + s.ph(3) + `, ` + s.ph(4) + `, ` + s.ph(5) + `)
ON CONFLICT (user_id)
DO UPDATE SET id=EXCLUDED.id, task_id=EXCLUDED.task_id, path=EXCLUDED.path, created_at=EXCLUDED.created_at`
	}
	return `INSERT INTO snapshots (user_id, id, task_id, path, created_at)
VALUES (` + s.ph(1) + `, ` + s.ph(2) + `, ` + s.ph(3) + `, ` + s.ph(4) + `, ` + s.ph(5) + `)
ON CONFLICT(user_id)
DO UPDATE SET id=excluded.id, task_id=excluded.task_id, path=excluded.path, created_at=excluded.created_at`
}

func (s *Store) selectSnapshotSQL() string {
	return `SELECT user_id, id, task_id, path, created_at FROM snapshots WHERE user_id=` + s.ph(1)
}

func (s *Store) upsertContainerSQL() string {
	if s.driver == "postgres" {
		return `INSERT INTO containers (id, state, task_id, updated_at)
VALUES (` + s.ph(1) + `, ` + s.ph(2) + `, ` + s.ph(3) + `, ` + s.ph(4) + `)
ON CONFLICT (id)
DO UPDATE SET state=EXCLUDED.state, task_id=EXCLUDED.task_id, updated_at=EXCLUDED.updated_at`
	}
	return `INSERT INTO containers (id, state, task_id, updated_at)
VALUES (` + s.ph(1) + `, ` + s.ph(2) + `, ` + s.ph(3) + `, ` + s.ph(4) + `)
ON CONFLICT(id)
DO UPDATE SET state=excluded.state, task_id=excluded.task_id, updated_at=excluded.updated_at`
}

func (s *Store) selectContainersSQL() string {
	return `SELECT id, state, task_id, updated_at FROM containers`
}

func (s *Store) selectEventsSQL(withTask bool) string {
	if withTask {
		return `SELECT id, task_id, from_status, to_status, reason, container_id, at FROM task_events WHERE task_id=` + s.ph(1) + ` ORDER BY at ASC`
	}
	return `SELECT id, task_id, from_status, to_status, reason, container_id, at FROM task_events ORDER BY at ASC`
}
