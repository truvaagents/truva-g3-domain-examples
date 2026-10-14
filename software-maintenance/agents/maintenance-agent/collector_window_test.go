package main

import (
	"testing"
	"time"
)

func TestCollectionWindowRemainsBoundedDuringAdmissionBacklog(t *testing.T) {
	for _, elapsed := range []time.Duration{time.Minute, 5 * time.Minute, 11 * time.Minute, time.Hour} {
		t.Run(elapsed.String(), func(t *testing.T) {
			agent := budgetAgent(t)
			started := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
			agent.now = func() time.Time { return started }
			agent.profile.Collection.WindowSeconds = 300
			agent.profile.Collection.OverlapSeconds = 60
			if _, err := agent.updateAdmissionBudget(t.Context(), func(*admissionBudget) error { return nil }); err != nil {
				t.Fatal(err)
			}
			// The first successful empty poll precedes the run's first admission.
			// Its overlap remains before StartedAt while pending work prevents progress.
			watermark := started.Add(50 * time.Second)
			key := agent.profile.CoordinatorProfileID + ":" + agent.profile.ProfileSHA256
			if err := agent.cases.setWatermark(t.Context(), key, watermark); err != nil {
				t.Fatal(err)
			}
			service := serviceProfileResponse{}
			service.ObservationConstraints.RetentionSeconds = 86400
			now := started.Add(elapsed)
			window, _, err := agent.collectionWindow(t.Context(), service, now)
			if err != nil {
				t.Fatal(err)
			}
			wantEnd := started.Add(5 * time.Minute)
			if now.Before(wantEnd) {
				wantEnd = now
			}
			if !window.Start.Equal(started) || !window.End.Equal(wantEnd) {
				t.Fatalf("window=%v..%v, want %v..%v", window.Start, window.End, started, wantEnd)
			}
			stored, err := agent.cases.getWatermark(t.Context(), key)
			if err != nil || !stored.Equal(watermark) {
				t.Fatalf("window calculation changed watermark: %v %v", stored, err)
			}
		})
	}
}
