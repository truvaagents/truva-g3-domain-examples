package main

import (
	"context"
	"fmt"

	"github.com/truvaagents/truva-g3/orchestration"
)

type hitlCommandProjection func(
	context.Context,
	*orchestration.ExecutionCheckpoint,
	*orchestration.Command,
	*orchestration.ResumeResult,
)

// hitlCommandProjectingController keeps application-owned task projection out
// of the framework HTTP handler. Embedding the framework controller preserves
// every InterruptController method while ProcessCommand adds the commerce
// example's terminal-task reconciliation after the authoritative command has
// succeeded.
type hitlCommandProjectingController struct {
	orchestration.InterruptController
	checkpointStore orchestration.CheckpointStore
	project         hitlCommandProjection
}

func newHITLCommandProjectingController(
	controller orchestration.InterruptController,
	checkpointStore orchestration.CheckpointStore,
	project hitlCommandProjection,
) orchestration.InterruptController {
	if controller == nil || checkpointStore == nil || project == nil {
		return controller
	}
	return &hitlCommandProjectingController{
		InterruptController: controller,
		checkpointStore:     checkpointStore,
		project:             project,
	}
}

func (c *hitlCommandProjectingController) ProcessCommand(
	ctx context.Context,
	command *orchestration.Command,
) (*orchestration.ResumeResult, error) {
	if command == nil {
		return c.InterruptController.ProcessCommand(ctx, command)
	}
	// Load the pre-command checkpoint payload used by the task projection.
	// The framework handler performs the same read for validation; keeping this
	// read here makes the decorator usable with any handler without coupling it
	// to framework-private request state.
	checkpoint, err := c.checkpointStore.LoadCheckpoint(ctx, command.CheckpointID)
	if err != nil {
		return nil, fmt.Errorf("load checkpoint for command projection: %w", err)
	}
	result, err := c.InterruptController.ProcessCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	c.project(ctx, checkpoint, command, result)
	return result, nil
}
