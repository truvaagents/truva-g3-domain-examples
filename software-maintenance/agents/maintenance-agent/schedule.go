package main

import (
	"context"
	"errors"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

func ensureCollectionSchedule(ctx context.Context, store core.ScheduleStore, profile coordinatorProfile, now time.Time) error {
	if store == nil {
		return errors.New("collection schedule store is required")
	}
	desiredInput := map[string]interface{}{
		"flow":                       collectionFlow,
		"coordinator_profile_id":     profile.CoordinatorProfileID,
		"coordinator_profile_sha256": profile.ProfileSHA256,
		"service_profile_id":         profile.ServiceProfileID,
	}
	existing, err := store.Get(ctx, profile.Collection.ScheduleID)
	if errors.Is(err, core.ErrScheduleNotFound) {
		nextMinute := now.UTC().Truncate(time.Minute).Add(time.Minute)
		return store.Create(ctx, &core.Schedule{
			ID: profile.Collection.ScheduleID, Input: desiredInput, TargetAgent: maintenanceAgentName,
			CronExpr: profile.Collection.CronExpression, RunAt: nextMinute, Enabled: profile.Collection.Enabled,
			MissedRunPolicy: core.MissedRunSkip, CreatedBy: maintenanceAgentName, CreatedAt: now.UTC(),
		})
	}
	if err != nil {
		return err
	}
	wasEnabled := existing.Enabled
	existing.Input = desiredInput
	existing.TargetAgent = maintenanceAgentName
	existing.CronExpr = profile.Collection.CronExpression
	existing.Enabled = profile.Collection.Enabled
	existing.MissedRunPolicy = core.MissedRunSkip
	existing.CreatedBy = maintenanceAgentName
	if existing.CreatedAt.IsZero() {
		existing.CreatedAt = now.UTC()
	}
	if profile.Collection.Enabled && (!wasEnabled || existing.RunAt.IsZero()) {
		existing.RunAt = now.UTC().Truncate(time.Minute).Add(time.Minute)
	}
	return store.Update(ctx, existing)
}
