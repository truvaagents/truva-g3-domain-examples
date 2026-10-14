package main

import (
	"regexp"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/maintenanceprofile"
)

var (
	coordinatorCaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	observationIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type coordinatorProfile = maintenanceprofile.Profile
type coordinatorLimits = maintenanceprofile.Limits

var loadCoordinatorProfile = maintenanceprofile.Load
var coordinatorSummaryLimit = maintenanceprofile.SummaryLimit
var validCoordinatorText = maintenanceprofile.ValidText
