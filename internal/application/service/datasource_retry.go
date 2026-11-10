package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type targetedSyncRetryKey struct{}

func (s *DataSourceService) markSyncInterrupted(ctx context.Context, log *types.SyncLog) {
	if log.Status == types.SyncLogStatusCanceled {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	log.Status = types.SyncLogStatusFailed
	log.FinishedAt = timePtr(time.Now().UTC())
	log.ErrorMessage = "同步执行中断，可再次同步"
	_ = s.syncLogRepo.UpdateResult(writeCtx, log)
}

func syncRetryScope(config *types.DataSourceConfig) string {
	base, _ := config.Settings["base_url"].(string)
	team, _ := config.Settings["team_id"].(string)
	if base == "" || team == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(base, "/")+"\x00"+team)))
}

func isTargetedSyncRetry(ctx context.Context) bool {
	value, _ := ctx.Value(targetedSyncRetryKey{}).(bool)
	return value
}

func syncRetryTarget(item *types.FetchedItem) types.SyncRetryTarget {
	t := types.SyncRetryTarget{Kind: item.Metadata["retry_kind"], ExternalID: item.ExternalID, ProjectID: item.Metadata["project_id"], BoardID: item.Metadata["board_id"], Title: item.Title, Phase: "ingest"}
	if item.IsDeleted {
		t.Phase = "delete"
	} else if item.Metadata["error"] != "" {
		t.Phase = "fetch"
	}
	if item.Metadata["retry_phase"] != "" {
		t.Phase = item.Metadata["retry_phase"]
	}
	if parts := strings.Split(item.ExternalID, ":"); len(parts) == 4 && parts[0] == "mockplus" {
		t.Kind = "board"
		t.ProjectID = parts[2]
		t.BoardID = parts[3]
	}
	if t.Kind == "" {
		t.Kind = "legacy"
	}
	if path := item.Metadata["folder_path"]; path != "" && item.Metadata["board_title"] != "" {
		t.Title = path + " / " + item.Metadata["board_title"]
	}
	return t
}

func (s *DataSourceService) RetryFailedSync(ctx context.Context, dsID, logID string) (*types.SyncLog, error) {
	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return nil, err
	}
	parent, err := s.syncLogRepo.FindByID(ctx, logID)
	if err != nil {
		return nil, err
	}
	if parent.DataSourceID != ds.ID || parent.TenantID != ds.TenantID {
		return nil, errors.New("同步记录不属于该数据源")
	}
	if ds.Type != types.ConnectorTypeMockplus {
		return nil, errors.New("该数据源暂不支持失败项重试")
	}
	if parent.Status != types.SyncLogStatusFailed && parent.Status != types.SyncLogStatusPartial {
		return nil, errors.New("该同步记录没有可重试的失败项")
	}
	if parent.ItemsFailed <= 0 {
		return nil, errors.New("没有可重试的失败项")
	}
	if busy, err := s.syncLogRepo.HasRunningSync(ctx, ds.ID); err != nil {
		return nil, err
	} else if busy {
		return nil, datasource.ErrSyncBusy
	}
	prior, err := parent.ParseResult()
	if err != nil {
		return nil, errors.New("失败记录无效，未触发全范围同步")
	}
	config, err := ds.ParseConfig()
	if err != nil {
		return nil, err
	}
	if prior != nil && prior.RetryScope != "" && prior.RetryScope != syncRetryScope(config) {
		return nil, errors.New("数据源连接或团队已更改，无法重试原同步记录")
	}
	initial := &types.SyncResult{RetryOfLogID: logID, RetryManifest: true}
	value, _ := initial.ToJSON()
	log := &types.SyncLog{DataSourceID: ds.ID, TenantID: ds.TenantID, Status: types.SyncLogStatusRunning, StartedAt: time.Now().UTC(), Result: value}
	if err := s.syncLogRepo.Create(ctx, log); err != nil {
		return nil, err
	}
	payload := types.DataSourceSyncPayload{DataSourceID: ds.ID, TenantID: ds.TenantID, SyncLogID: log.ID, RetryOfLogID: logID, Trigger: "retry_failed", Initiator: types.TaskInitiatorFromContext(ctx)}
	body, _ := json.Marshal(payload)
	_, err = s.taskEnqueuer.Enqueue(asynq.NewTask(types.TypeDataSourceSync, body), asynq.Queue(types.QueueSync), asynq.MaxRetry(5), asynq.Timeout(2*time.Hour))
	if err != nil {
		log.Status = types.SyncLogStatusFailed
		log.ErrorMessage = "失败项重试未能加入队列"
		log.FinishedAt = timePtr(time.Now().UTC())
		_ = s.syncLogRepo.UpdateResult(ctx, log)
		return nil, err
	}
	return log, nil
}

func retryTargets(result *types.SyncResult, failed int) ([]types.SyncRetryTarget, int) {
	if result == nil {
		return nil, failed
	}
	if result.RetryManifest {
		return result.RetryTargets, result.UnresolvedFailures
	}
	targets := []types.SyncRetryTarget{}
	for _, e := range result.Errors {
		if len(targets) >= failed {
			break
		}
		targets = append(targets, types.SyncRetryTarget{Kind: "legacy", Title: e.Title, Phase: "fetch"})
	}
	return targets, max(0, failed-len(targets))
}

func (s *DataSourceService) processFailedSyncRetry(ctx context.Context, ds *types.DataSource, log *types.SyncLog, config *types.DataSourceConfig, payload types.DataSourceSyncPayload, wasPaused bool, connector datasource.Connector) (runErr error) {
	defer func() {
		if runErr != nil && ctx.Err() == nil {
			log.Status = types.SyncLogStatusFailed
			log.ErrorMessage = "失败项重试未完成，请检查连接后重试原失败记录"
			log.FinishedAt = timePtr(time.Now().UTC())
			_ = s.syncLogRepo.UpdateResult(ctx, log)
		}
	}()
	ctx = context.WithValue(ctx, targetedSyncRetryKey{}, true)
	parent, err := s.syncLogRepo.FindByID(ctx, payload.RetryOfLogID)
	if err != nil || parent == nil || parent.DataSourceID != ds.ID || parent.TenantID != ds.TenantID {
		return fmt.Errorf("%w: invalid retry source", asynq.SkipRetry)
	}
	prior, err := parent.ParseResult()
	if err != nil {
		return fmt.Errorf("%w: invalid retry result", asynq.SkipRetry)
	}
	if prior != nil && prior.RetryScope != "" && prior.RetryScope != syncRetryScope(config) {
		return fmt.Errorf("%w: retry source scope changed", asynq.SkipRetry)
	}
	targets, missing := retryTargets(prior, parent.ItemsFailed)
	result := &types.SyncResult{RetryOfLogID: parent.ID, RetryManifest: true, RetryScope: syncRetryScope(config), SyncLogID: log.ID, UnresolvedFailures: missing, Failed: missing, Total: missing}
	if missing > 0 {
		recordSyncError(result, types.SyncItemError{Message: fmt.Sprintf("旧日志未保存 %d 项失败目标，未扩大重试范围", missing)})
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, ds.TenantID)
	tenant, err := s.tenantRepo.GetTenantByID(ctx, ds.TenantID)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	cursor, err := ds.ParseSyncCursor()
	if err != nil {
		return err
	}
	handler := &streamSyncHandler{svc: s, ds: ds, tagIDs: s.resolveAutoTagIDs(ctx, ds), result: result, syncLog: log}
	sc, ok := connector.(datasource.FailedItemsConnector)
	if !ok {
		return fmt.Errorf("%w: retry connector unavailable", asynq.SkipRetry)
	}
	if len(targets) > 0 {
		next, fetchErr := sc.FetchRetryStream(ctx, config, cursor, targets, handler)
		if fetchErr != nil {
			// A source-wide failure must retain the full manifest for another try.
			result.RetryTargets = targets
			result.Failed = max(result.Failed, len(targets)+missing)
			result.Total = max(result.Total, result.Failed)
			recordSyncError(result, types.SyncItemError{Message: fetchErr.Error()})
		} else if next != nil {
			ds.LastSyncCursor, _ = next.ToJSON()
		}
	}
	status, message := types.SyncLogStatusSuccess, ""
	if result.Failed > 0 {
		status = types.SyncLogStatusPartial
		message = fmt.Sprintf("%d 项未完成同步", result.Failed)
	}
	value, _ := result.ToJSON()
	// The historical record is immutable. Updating its retry never clears a
	// newer full-sync result or changes the time of the last whole-scope sync.
	current, _ := ds.ParseSyncResult()
	matching := current != nil && (current.SyncLogID == parent.ID || current.RetryOfLogID == parent.ID)
	if current != nil && current.SyncLogID == "" && ds.LastSyncAt != nil && parent.FinishedAt != nil {
		matching = ds.LastSyncAt.Sub(*parent.FinishedAt).Abs() < time.Second
	}
	if matching {
		s.updateSyncRunResult(ctx, ds, log, result, value, status, message, wasPaused)
	} else {
		log.Status = status
		log.FinishedAt = timePtr(time.Now().UTC())
		log.ErrorMessage = message
		log.Result = value
		log.ItemsTotal = result.Total
		log.ItemsCreated = result.Created
		log.ItemsUpdated = result.Updated
		log.ItemsSkipped = result.Skipped
		log.ItemsFailed = result.Failed
		if err := s.syncLogRepo.UpdateResult(ctx, log); err != nil {
			return err
		}
	}
	return nil
}

// All sync modes share a renewable lease. Waiting tasks re-read the latest
// log after acquiring it, so a delayed older task cannot overwrite newer work.
func (s *DataSourceService) acquireSyncLease(ctx context.Context, dsID, logID string) (context.Context, func(), error) {
	if s.redisClient == nil {
		return ctx, func() {}, nil
	}
	key, token := "weknora:datasource-sync:"+dsID, logID+":"+uuid.NewString()
	for {
		ok, err := s.redisClient.SetNX(ctx, key, token, 60*time.Second).Result()
		if err != nil {
			return ctx, nil, err
		}
		if ok {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx, nil, ctx.Err()
		case <-timer.C:
		}
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, end := context.WithTimeout(leaseCtx, 5*time.Second)
				n, err := s.redisClient.Eval(renewCtx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('EXPIRE',KEYS[1],60) end return 0`, []string{key}, token).Int()
				end()
				if err != nil || n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return leaseCtx, func() {
		close(stop)
		cancel()
		<-done
		releaseCtx, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		s.redisClient.Eval(releaseCtx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{key}, token)
	}, nil
}
