// Administrative recovery of only failed stages. No reparse/delete API calls.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

type routing struct {
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	TenantID        uint64 `json:"tenant_id"`
	Attempt         int    `json:"attempt"`
	ChunkID         string `json:"chunk_id"`
	ImageURL        string `json:"image_url"`
}
type candidate struct {
	task    *asynq.TaskInfo
	route   routing
	doneKey string
}

func signature(task *asynq.TaskInfo) string {
	var route routing
	_ = json.Unmarshal(task.Payload, &route)
	if task.Type == types.TypeImageMultimodal {
		return fmt.Sprintf("%s:%d:%s:%s", route.KnowledgeID, route.Attempt, route.ChunkID, route.ImageURL)
	}
	return task.Type + ":" + route.KnowledgeID
}
func openDB() (*sql.DB, error) {
	u := &url.URL{Scheme: "postgres", Host: os.Getenv("DB_HOST") + ":" + os.Getenv("DB_PORT"), Path: os.Getenv("DB_NAME"), User: url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"))}
	q := url.Values{"sslmode": {"disable"}}
	u.RawQuery = q.Encode()
	return sql.Open("pgx", u.String())
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Recovery stopped:", err)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_DB"))
	opt := asynq.RedisClientOpt{Addr: os.Getenv("REDIS_ADDR"), Password: os.Getenv("REDIS_PASSWORD"), DB: redisDB}
	in := asynq.NewInspector(opt)
	defer in.Close()
	client := asynq.NewClient(opt)
	defer client.Close()
	rdb := redis.NewClient(&redis.Options{Addr: opt.Addr, Password: opt.Password, DB: opt.DB})
	defer rdb.Close()
	mode := "status"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "status" {
		queues, _ := in.Queues()
		for _, queue := range queues {
			stat, e := in.GetQueueInfo(queue)
			if e != nil {
				return e
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"queue": queue, "pending": stat.Pending, "active": stat.Active, "retry": stat.Retry, "archived": stat.Archived, "processed": stat.Processed, "failed": stat.Failed})
		}
		return nil
	}
	limit := 10
	if mode == "batch" {
		limit = 25
	} else if mode != "pilot" && mode != "preview" && mode != "files" && mode != "reconcile" && mode != "prioritize" {
		return fmt.Errorf("invalid mode")
	}
	lock := "parse:recovery:lock"
	token := fmt.Sprint(time.Now().UnixNano())
	ok, e := rdb.SetNX(ctx, lock, token, 10*time.Minute).Result()
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("another recovery is active")
	}
	defer rdb.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{lock}, token)
	queues, _ := in.Queues()
	live := map[string]bool{}
	liveImages := map[string]routing{}
	liveImageTasks := map[string]*asynq.TaskInfo{}
	for _, queue := range queues {
		for _, list := range []func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error){in.ListPendingTasks, in.ListActiveTasks, in.ListScheduledTasks, in.ListRetryTasks} {
			for page := 1; ; page++ {
				tasks, e := list(queue, asynq.PageSize(100), asynq.Page(page))
				if e != nil {
					return e
				}
				for _, task := range tasks {
					live[signature(task)] = true
					if task.Type == types.TypeImageMultimodal {
						var route routing
						if json.Unmarshal(task.Payload, &route) == nil {
							liveImages[signature(task)] = route
							liveImageTasks[signature(task)] = task
						}
					}
				}
				if len(tasks) < 100 {
					break
				}
			}
		}
	}
	if mode == "prioritize" {
		if len(os.Args) != 3 {
			return fmt.Errorf("knowledge ID required")
		}
		for sig, p := range liveImages {
			if p.KnowledgeID != os.Args[2] {
				continue
			}
			task := liveImageTasks[sig]
			if task.State != asynq.TaskStatePending {
				continue
			}
			var eligible bool
			if e := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledges k JOIN chunks c ON c.knowledge_id=k.id WHERE k.id=$1 AND k.tenant_id=$2 AND k.deleted_at IS NULL AND k.parse_status='processing' AND c.id=$3 AND c.deleted_at IS NULL)`, p.KnowledgeID, p.TenantID, p.ChunkID).Scan(&eligible); e != nil {
				return e
			}
			if !eligible {
				continue
			}
			moved, e := rdb.Eval(ctx, `if redis.call('LREM',KEYS[1],1,ARGV[1])==1 then redis.call('RPUSH',KEYS[1],ARGV[1]);return 1 end return 0`, []string{"asynq:{" + task.Queue + "}:pending"}, task.ID).Int()
			if e != nil {
				return e
			}
			if moved == 1 {
				json.NewEncoder(os.Stdout).Encode(map[string]any{"action": "prioritized_existing_task", "knowledge_id": p.KnowledgeID, "task_id": task.ID})
			}
		}
		return nil
	}
	if mode == "reconcile" {
		leases := rdb.Scan(ctx, 0, "multimodal:done:*:lease", 100).Iterator()
		for leases.Next(ctx) {
			if e := rdb.Del(ctx, leases.Val()).Err(); e != nil {
				return e
			}
		}
		if e := leases.Err(); e != nil {
			return e
		}
		// Call only while the app workers are stopped. Rebuild counters from
		// durable unfinished image tasks, retaining files/chunks/Wiki intact.
		rows, e := db.QueryContext(ctx, `SELECT id,parse_status FROM knowledges WHERE deleted_at IS NULL`)
		if e != nil {
			return e
		}
		states := map[string]string{}
		for rows.Next() {
			var id, state string
			if e := rows.Scan(&id, &state); e != nil {
				rows.Close()
				return e
			}
			states[id] = state
		}
		rows.Close()
		counts := map[string]int{}
		for _, p := range liveImages {
			state := states[p.KnowledgeID]
			if state != "processing" && state != "failed" {
				continue
			}
			identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s", p.KnowledgeID, p.Attempt, p.ChunkID, p.ImageURL)
			key := fmt.Sprintf("multimodal:done:%x", sha256.Sum256([]byte(identity)))
			done, e := rdb.Exists(ctx, key).Result()
			if e != nil {
				return e
			}
			if done == 0 {
				counts[p.KnowledgeID]++
			}
		}
		for id, count := range counts {
			if e := rdb.Set(ctx, "multimodal:pending:"+id, count, 0).Err(); e != nil {
				return e
			}
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"reconciled_image_files": len(counts), "live_unique_images": len(liveImages)})
		return nil
	}
	selected := []candidate{}
	for _, queue := range []string{"multimodal", "summary", "question"} {
		for page := 1; len(selected) < limit; page++ {
			tasks, e := in.ListArchivedTasks(queue, asynq.PageSize(100), asynq.Page(page))
			if e != nil {
				return e
			}
			for _, task := range tasks {
				if len(selected) >= limit {
					break
				}
				if (!strings.Contains(task.LastErr, "DNS resolution failed") && !strings.Contains(task.LastErr, "Invalid xf")) || live[signature(task)] {
					continue
				}
				if task.Type != types.TypeImageMultimodal && task.Type != types.TypeSummaryGeneration && task.Type != types.TypeQuestionGeneration && task.Type != types.TypeDataTableSummary {
					continue
				}
				var p routing
				if json.Unmarshal(task.Payload, &p) != nil || p.KnowledgeID == "" {
					continue
				}
				if p.KnowledgeBaseID == "" && task.Type == types.TypeDataTableSummary {
					e := db.QueryRowContext(ctx, `SELECT knowledge_base_id FROM knowledges WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, p.KnowledgeID, p.TenantID).Scan(&p.KnowledgeBaseID)
					if e == sql.ErrNoRows {
						continue
					}
					if e != nil {
						return e
					}
				}
				var status string
				var latest int
				e = db.QueryRowContext(ctx, `SELECT parse_status,COALESCE((SELECT max(attempt) FROM knowledge_processing_spans s WHERE s.knowledge_id=k.id),0) FROM knowledges k WHERE id=$1 AND tenant_id=$2 AND knowledge_base_id=$3 AND deleted_at IS NULL`, p.KnowledgeID, p.TenantID, p.KnowledgeBaseID).Scan(&status, &latest)
				if e == sql.ErrNoRows {
					continue
				}
				if e != nil {
					return e
				}
				if status == "completed" || status == "cancelled" || status == "deleting" {
					continue
				}
				if p.Attempt > 0 && latest > p.Attempt {
					continue
				}
				if task.Type == types.TypeImageMultimodal && status == "finalizing" {
					continue
				}
				if (task.Type == types.TypeSummaryGeneration || task.Type == types.TypeQuestionGeneration) && status != "finalizing" {
					continue
				}
				if mode == "files" && status != "failed" {
					continue
				}
				identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s", p.KnowledgeID, p.Attempt, p.ChunkID, p.ImageURL)
				selected = append(selected, candidate{task, p, fmt.Sprintf("multimodal:done:%x", sha256.Sum256([]byte(identity)))})
				live[signature(task)] = true
			}
			if len(tasks) < 100 {
				break
			}
		}
	}
	if mode == "preview" {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"eligible_pilot": len(selected)})
		return nil
	}
	// Reserve all recovered images before any task starts, so sibling completion
	// cannot finish the parent before the rest of this batch was requeued.
	reserved := []candidate{}
	for _, c := range selected {
		if c.task.Type == types.TypeImageMultimodal {
			res, e := db.ExecContext(ctx, `UPDATE knowledges SET parse_status='processing',error_message='' WHERE id=$1 AND tenant_id=$2 AND parse_status IN ('processing','failed') AND deleted_at IS NULL`, c.route.KnowledgeID, c.route.TenantID)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				continue
			}
			_, e = rdb.Eval(ctx, `local n=tonumber(redis.call('GET',KEYS[1])) or 0; if n<0 then n=0 end;redis.call('SET',KEYS[1],n+1);redis.call('DEL',KEYS[2],KEYS[3]);return n+1`, []string{"multimodal:pending:" + c.route.KnowledgeID, c.doneKey, c.doneKey + ":ready"}).Result()
			if e != nil {
				return e
			}
		} else if c.task.Type == types.TypeSummaryGeneration || c.task.Type == types.TypeQuestionGeneration {
			res, e := db.ExecContext(ctx, `UPDATE knowledges SET pending_subtasks_count=pending_subtasks_count+1 WHERE id=$1 AND tenant_id=$2 AND parse_status='finalizing' AND deleted_at IS NULL`, c.route.KnowledgeID, c.route.TenantID)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				continue
			}
		} else {
			var status string
			if e := db.QueryRowContext(ctx, `SELECT parse_status FROM knowledges WHERE id=$1 AND deleted_at IS NULL`, c.route.KnowledgeID).Scan(&status); e != nil {
				return e
			}
			if status == "completed" || status == "cancelled" || status == "deleting" {
				continue
			}
		}
		reserved = append(reserved, c)
	}
	selected = reserved
	for _, c := range selected {
		if e := in.RunTask(c.task.Queue, c.task.ID); e != nil {
			return e
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"action": "recovered_failed_stage", "task_id": c.task.ID, "knowledge_id": c.route.KnowledgeID, "queue": c.task.Queue})
	}
	if mode == "files" {
		// A housekeeping-failed image can still have durable queued work.
		// Resume its owning stage in place instead of enqueuing a duplicate.
		resumed := map[string]bool{}
		for _, p := range liveImages {
			if resumed[p.KnowledgeID] {
				continue
			}
			res, e := db.ExecContext(ctx, `UPDATE knowledges SET parse_status='processing',error_message='' WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL AND parse_status='failed' AND error_message LIKE 'task stuck%multimodal%' AND EXISTS(SELECT 1 FROM chunks c WHERE c.id=$3 AND c.knowledge_id=$1 AND c.deleted_at IS NULL)`, p.KnowledgeID, p.TenantID, p.ChunkID)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n == 1 {
				resumed[p.KnowledgeID] = true
				json.NewEncoder(os.Stdout).Encode(map[string]any{"action": "resumed_existing_image_stage", "knowledge_id": p.KnowledgeID})
			}
		}
		// Only documents with no surviving chunks may restart parsing. A file
		// with existing results never goes through destructive processChunks.
		rows, e := db.QueryContext(ctx, `SELECT k.id,k.tenant_id,k.knowledge_base_id,k.file_path,k.file_name,k.file_type,COALESCE((SELECT max(attempt) FROM knowledge_processing_spans s WHERE s.knowledge_id=k.id),1),COALESCE((b.vlm_config->>'enabled')::boolean,false),COALESCE((b.question_generation_config->>'enabled')::boolean,false),COALESCE((b.question_generation_config->>'question_count')::int,3) FROM knowledges k JOIN knowledge_bases b ON b.id=k.knowledge_base_id WHERE k.deleted_at IS NULL AND k.parse_status='failed' AND k.error_message LIKE 'task stuck%chunking%' AND NOT EXISTS(SELECT 1 FROM chunks c WHERE c.knowledge_id=k.id AND c.deleted_at IS NULL)`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p types.DocumentProcessPayload
			if e := rows.Scan(&p.KnowledgeID, &p.TenantID, &p.KnowledgeBaseID, &p.FilePath, &p.FileName, &p.FileType, &p.Attempt, &p.EnableMultimodel, &p.EnableQuestionGeneration, &p.QuestionCount); e != nil {
				return e
			}
			p.Language = "zh-CN"
			raw, _ := json.Marshal(p)
			task := &asynq.TaskInfo{Type: types.TypeDocumentProcess, Payload: raw}
			if live[signature(task)] {
				continue
			}
			if _, e := client.Enqueue(asynq.NewTask(task.Type, raw), asynq.Queue(types.QueueDefault), asynq.MaxRetry(3), asynq.Timeout(2*time.Hour), asynq.TaskID("recover-stage-"+p.KnowledgeID)); e != nil {
				return e
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"action": "resumed_unfinished_chunking", "knowledge_id": p.KnowledgeID})
		}
		if e := rows.Err(); e != nil {
			return e
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"recovered": len(selected), "batch_limit": limit})
	return nil
}
