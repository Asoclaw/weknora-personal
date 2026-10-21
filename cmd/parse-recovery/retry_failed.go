package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
)

// Explicit operator recovery. Original files, chunk IDs and Wiki rows stay intact.
func retryFailed(ctx context.Context, db *sql.DB, client *asynq.Client, live map[string]bool, images map[string]routing) error {
	rows, err := db.QueryContext(ctx, `SELECT k.id,k.tenant_id,k.knowledge_base_id,k.file_path,k.file_name,k.file_type,k.error_message,COALESCE((SELECT max(attempt) FROM knowledge_processing_spans s WHERE s.knowledge_id=k.id),1),COALESCE((b.vlm_config->>'enabled')::boolean,false),COALESCE((b.question_generation_config->>'enabled')::boolean,false),COALESCE((b.question_generation_config->>'question_count')::int,3),(SELECT count(*) FROM chunks c WHERE c.knowledge_id=k.id AND c.deleted_at IS NULL) FROM knowledges k JOIN knowledge_bases b ON b.id=k.knowledge_base_id AND b.tenant_id=k.tenant_id AND b.deleted_at IS NULL WHERE k.deleted_at IS NULL AND k.parse_status='failed' ORDER BY k.id`)
	if err != nil {
		return err
	}
	type item struct {
		p      types.DocumentProcessPayload
		reason string
		n      int
	}
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.p.KnowledgeID, &x.p.TenantID, &x.p.KnowledgeBaseID, &x.p.FilePath, &x.p.FileName, &x.p.FileType, &x.reason, &x.p.Attempt, &x.p.EnableMultimodel, &x.p.EnableQuestionGeneration, &x.p.QuestionCount, &x.n); err != nil {
			return err
		}
		x.p.Language = "zh-CN"
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	emit := func(id, action string) {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"knowledge_id": id, "action": action})
	}
	for _, x := range items {
		id := x.p.KnowledgeID
		if x.n == 0 {
			raw, _ := json.Marshal(x.p)
			task := &asynq.TaskInfo{Type: types.TypeDocumentProcess, Payload: raw}
			if live[signature(task)] {
				emit(id, "existing_document_task")
				continue
			}
			_, err = client.Enqueue(asynq.NewTask(task.Type, raw), asynq.Queue(types.QueueDefault), asynq.MaxRetry(3), asynq.Timeout(2*time.Hour), asynq.TaskID("retry-failed-20261016-"+id))
			if err != nil {
				return err
			}
			emit(id, "queued_document_without_existing_chunks")
			continue
		}
		if strings.Contains(x.reason, "multimodal") {
			found := false
			for _, p := range images {
				if p.KnowledgeID == id {
					found = true
				}
			}
			if !found {
				var ready bool
				if err = db.QueryRowContext(ctx, `SELECT count(DISTINCT c.chunk_type)=2 AND bool_and(EXISTS(SELECT 1 FROM embeddings e WHERE e.chunk_id=c.id)) FROM chunks c WHERE c.knowledge_id=$1 AND c.deleted_at IS NULL AND c.chunk_type IN ('image_ocr','image_caption')`, id).Scan(&ready); err != nil {
					return err
				}
				if !ready {
					emit(id, "needs_missing_image_task")
					continue
				}
				_, err = db.ExecContext(ctx, `UPDATE knowledges SET parse_status='processing',error_message='',updated_at=now() WHERE id=$1 AND parse_status='failed'`, id)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(types.KnowledgePostProcessPayload{TenantID: x.p.TenantID, KnowledgeID: id, KnowledgeBaseID: x.p.KnowledgeBaseID, Attempt: x.p.Attempt, Language: "zh-CN"})
				_, err = client.Enqueue(asynq.NewTask(types.TypeKnowledgePostProcess, raw), asynq.Queue(types.QueuePostProcess), asynq.MaxRetry(3), asynq.Timeout(time.Hour), asynq.TaskID("retry-image-post-20261016-"+id))
				if err != nil {
					return err
				}
				emit(id, "resumed_postprocess_existing_image_results")
				continue
			}
			_, err = db.ExecContext(ctx, `UPDATE knowledges SET parse_status='processing',error_message='',updated_at=now() WHERE id=$1 AND parse_status='failed'`, id)
			if err != nil {
				return err
			}
			emit(id, "resumed_existing_image_task")
			continue
		}
		if strings.Contains(x.reason, "postprocess.wiki") {
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(map[string]string{"op": "ingest", "knowledge_id": id, "language": "zh-CN"})
			_, e = tx.ExecContext(ctx, `INSERT INTO task_pending_ops(tenant_id,task_type,scope,scope_id,op,dedup_key,payload,fail_count,enqueued_at) SELECT $1::bigint,'wiki:ingest','knowledge_base',$2::text,'ingest',$3::text,$4::jsonb,0,now() WHERE NOT EXISTS(SELECT 1 FROM task_pending_ops WHERE task_type='wiki:ingest' AND tenant_id=$1 AND scope_id=$2 AND dedup_key=$3)`, x.p.TenantID, x.p.KnowledgeBaseID, id, string(raw))
			if e != nil {
				tx.Rollback()
				return e
			}
			_, e = tx.ExecContext(ctx, `UPDATE knowledges SET parse_status='finalizing',pending_subtasks_count=1,error_message='',updated_at=now() WHERE id=$1 AND parse_status='failed'`, id)
			if e != nil {
				tx.Rollback()
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			raw, _ = json.Marshal(map[string]any{"tenant_id": x.p.TenantID, "knowledge_base_id": x.p.KnowledgeBaseID})
			_, err = client.Enqueue(asynq.NewTask(types.TypeWikiIngest, raw), asynq.Queue(types.QueueWiki), asynq.MaxRetry(3), asynq.Timeout(time.Hour), asynq.TaskID("retry-wiki-20261016-"+id))
			if err != nil {
				return err
			}
			emit(id, "resumed_wiki_only")
			continue
		}
		// Tables already have successful summary/column chunks. Re-index only those
		// surviving chunks, never run processChunks' destructive replacement path.
		if x.p.FileType == "xlsx" {
			if err = retryTableIndex(ctx, db, x.p); err != nil {
				return fmt.Errorf("table stage %s: %w", id, err)
			}
			emit(id, "resumed_existing_table_index_and_postprocess")
			continue
		}
		emit(id, "requires_stage_review")
	}
	return nil
}

func retryTableIndex(ctx context.Context, db *sql.DB, p types.DocumentProcessPayload) error {
	var m types.Model
	var params []byte
	if err := db.QueryRowContext(ctx, `SELECT m.id,m.name,m.source,m.parameters FROM models m JOIN knowledge_bases b ON b.embedding_model_id=m.id WHERE b.id=$1 AND b.tenant_id=$2 AND m.deleted_at IS NULL`, p.KnowledgeBaseID, p.TenantID).Scan(&m.ID, &m.Name, &m.Source, &params); err != nil {
		return err
	}
	if err := json.Unmarshal(params, &m.Parameters); err != nil {
		return err
	}
	key, err := utils.DecryptStoredSecret(m.Parameters.APIKey)
	if err != nil {
		return err
	}
	m.Parameters.APIKey = key
	e, err := embedding.NewEmbedder(embedding.ConfigFromModel(&m, "", ""), nil, nil)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.content,c.chunk_type,EXISTS(SELECT 1 FROM embeddings v WHERE v.chunk_id=c.id) FROM chunks c WHERE c.knowledge_id=$1 AND c.tenant_id=$2 AND c.deleted_at IS NULL`, p.KnowledgeID, p.TenantID)
	if err != nil {
		return err
	}
	type chunk struct {
		id, content, kind string
		indexed           bool
	}
	var chunks []chunk
	for rows.Next() {
		var c chunk
		if err = rows.Scan(&c.id, &c.content, &c.kind, &c.indexed); err != nil {
			rows.Close()
			return err
		}
		if c.kind != "table_summary" && c.kind != "table_column" {
			rows.Close()
			return fmt.Errorf("unexpected existing chunk type")
		}
		chunks = append(chunks, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no existing table results")
	}
	for _, c := range chunks {
		if c.indexed {
			continue
		}
		vec, er := e.Embed(ctx, c.content)
		if er != nil {
			return er
		}
		raw, _ := json.Marshal(vec)
		_, er = db.ExecContext(ctx, `INSERT INTO embeddings(source_id,source_type,chunk_id,knowledge_id,knowledge_base_id,content,dimension,embedding,is_enabled) VALUES($1,0,$1,$2,$3,$4,$5,$6::vector,true) ON CONFLICT(source_id,source_type) DO NOTHING`, c.id, p.KnowledgeID, p.KnowledgeBaseID, c.content, len(vec), string(raw))
		if er != nil {
			return er
		}
	}
	raw, _ := json.Marshal(types.KnowledgePostProcessPayload{KnowledgeID: p.KnowledgeID, KnowledgeBaseID: p.KnowledgeBaseID, TenantID: p.TenantID, Attempt: p.Attempt, Language: "zh-CN"})
	opt := asynq.RedisClientOpt{Addr: os.Getenv("REDIS_ADDR"), Password: os.Getenv("REDIS_PASSWORD")}
	fmt.Sscan(os.Getenv("REDIS_DB"), &opt.DB)
	client := asynq.NewClient(opt)
	defer client.Close()
	_, err = db.ExecContext(ctx, `UPDATE knowledges SET parse_status='processing',processed_at=COALESCE(processed_at,now()),error_message='',updated_at=now() WHERE id=$1 AND tenant_id=$2 AND parse_status='failed'`, p.KnowledgeID, p.TenantID)
	if err != nil {
		return err
	}
	_, err = client.Enqueue(asynq.NewTask(types.TypeKnowledgePostProcess, raw), asynq.Queue(types.QueuePostProcess), asynq.MaxRetry(3), asynq.Timeout(time.Hour), asynq.TaskID("retry-table-post-20261016-"+p.KnowledgeID))
	return err
}
