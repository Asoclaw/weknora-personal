package service

import "context"

// Fixed safeguards for this deployment: acquire before loading file bytes.
var fileProcessingSlots = make(chan struct{}, 1)
var imageProcessingSlots = make(chan struct{}, 2)

func acquireProcessingSlot(ctx context.Context, slots chan struct{}) (func(), error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
