package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type collectionDispatcher struct {
	queue              core.TaskQueue
	targetAgent        string
	coordinatorProfile string
	coordinatorSHA256  string
	serviceProfile     string
}

func (dispatcher *collectionDispatcher) Dispatch(ctx context.Context, queueName string, task *core.Task) error {
	if dispatcher == nil || dispatcher.queue == nil {
		return errors.New("collection dispatcher queue is unavailable")
	}
	if queueName != orchestration.ScheduledExecutorQueue {
		return fmt.Errorf("collection dispatcher rejected queue %q", queueName)
	}
	if task == nil || task.Type != core.ScheduledTaskType || task.TargetAgent != dispatcher.targetAgent {
		return errors.New("collection dispatcher rejected scheduled task identity")
	}
	flow, _ := task.Input["flow"].(string)
	coordinatorProfile, _ := task.Input["coordinator_profile_id"].(string)
	coordinatorSHA256, _ := task.Input["coordinator_profile_sha256"].(string)
	serviceProfile, _ := task.Input["service_profile_id"].(string)
	if flow != collectionFlow || coordinatorProfile != dispatcher.coordinatorProfile ||
		coordinatorSHA256 != dispatcher.coordinatorSHA256 || serviceProfile != dispatcher.serviceProfile {
		return errors.New("collection dispatcher rejected scheduled task scope")
	}
	return dispatcher.queue.Enqueue(ctx, task)
}
