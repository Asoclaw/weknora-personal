package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestProcessingBudgetBlocksAndCancelsBeforeWork(t *testing.T) {
	for _, capacity := range []int{1, 2} {
		slots := make(chan struct{}, capacity)
		releases := []func(){}
		for i := 0; i < capacity; i++ {
			release, err := acquireProcessingSlot(context.Background(), slots)
			if err != nil {
				t.Fatal(err)
			}
			releases = append(releases, release)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		if _, err := acquireProcessingSlot(ctx, slots); err == nil {
			t.Fatal("over-budget work admitted")
		}
		cancel()
		for _, release := range releases {
			release()
		}
	}
}

func TestImageCompletionIsOncePerImageAndAttempt(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	enqueue := &orphanTaskEnqueuer{}
	svc := &ImageMultimodalService{redisClient: rdb, taskEnqueuer: enqueue}
	ctx := context.Background()
	p := types.ImageMultimodalPayload{KnowledgeID: "knowledge", ChunkID: "chunk", ImageURL: "local://image", Attempt: 1}
	rdb.Set(ctx, multimodalPendingKey(p.KnowledgeID), 2, 0)
	for i := 0; i < 2; i++ {
		if err := svc.checkAndFinalizeAllImages(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := rdb.Get(ctx, multimodalPendingKey(p.KnowledgeID)).Int(); n != 1 || len(enqueue.enqueued) != 0 {
		t.Fatal("duplicate counted or early finalize")
	}
	p.ImageURL = "local://second"
	for i := 0; i < 2; i++ {
		svc.checkAndFinalizeAllImages(ctx, p)
	}
	if len(enqueue.enqueued) != 1 || mr.Exists(multimodalPendingKey(p.KnowledgeID)) {
		t.Fatal("finalize must occur exactly once")
	}
	p.Attempt = 2
	rdb.Set(ctx, multimodalPendingKey(p.KnowledgeID), 1, 0)
	svc.checkAndFinalizeAllImages(ctx, p)
	if len(enqueue.enqueued) != 2 {
		t.Fatal("new attempt was blocked by old marker")
	}
}
