package serviceevidence

import (
	"context"
	"errors"
	"time"
)

var errReleaseChanged = errors.New("serving release changed during configuration read")

func readEvidence(ctx context.Context, profile serviceProfile, client evidenceClient, now func() time.Time, value string) (evidenceResponse, error) {
	receipt, err := client.getEvidence(ctx, profile, value)
	if err != nil {
		return evidenceResponse{}, err
	}
	response := evidenceResponse{
		ProfileID:      profile.ProfileID,
		ProfileVersion: profile.version,
		Observation: evidenceObservation{
			ObservationID: receipt.ObservationID, OrderID: receipt.OrderID, Operation: receipt.Operation,
			RequestedPromotionCode: receipt.RequestedPromotionCode,
			ObservedAt:             receipt.At, ServingRevision: receipt.Revision, OrderRevision: receipt.OrderRevision,
			TargetTraceID: receipt.TraceID, HTTPStatus: receipt.HTTPStatus, ErrorCode: receipt.ErrorCode,
			SKU: receipt.SKU, UnitPrice: receipt.UnitPrice, Quantity: receipt.Quantity,
			Discount: receipt.Discount, Currency: receipt.Currency, LedgerTotal: receipt.LedgerTotal,
			LedgerWrites: receipt.LedgerWrites,
		},
		Provenance:  provenance{Source: profile.ServiceName, CollectedAt: now()},
		Limitations: []string{},
	}
	if receipt.HTTPStatus >= 200 && receipt.HTTPStatus < 300 && receipt.OrderID != "" {
		response.OutcomeReference = &outcomeReference{OutcomeID: receipt.OrderID, Kind: "retail-order"}
	}
	if receipt.HTTPStatus < 200 || receipt.HTTPStatus >= 300 {
		response.Limitations = append(response.Limitations, "Rejected attempts do not inherit an accepted order outcome.")
	}
	return response, nil
}

func readOutcome(ctx context.Context, profile serviceProfile, client evidenceClient, now func() time.Time, value string) (outcomeResponse, error) {
	order, err := client.getOutcome(ctx, profile, value)
	if err != nil {
		return outcomeResponse{}, err
	}
	return outcomeResponse{
		ProfileID: profile.ProfileID, ProfileVersion: profile.version, OutcomeID: value,
		Request: order.Request, RequestSHA256: order.RequestSHA256,
		PricingReceipt: pricingReceipt{
			OrderRevision: order.Receipt.Revision, UnitPrice: order.Receipt.UnitPrice,
			Discount: order.Receipt.Discount, Currency: order.Receipt.Currency,
		},
		Ledger:     outcomeLedger{TotalCents: order.Ledger.Total, Writes: order.Ledger.Writes},
		Provenance: provenance{Source: profile.ServiceName, CollectedAt: now()},
	}, nil
}

func readRelease(ctx context.Context, profile serviceProfile, client evidenceClient, now func() time.Time, value string) (releaseResponse, error) {
	release, err := client.getBuildInfo(ctx, profile)
	if err != nil {
		return releaseResponse{}, err
	}
	response := releaseResponse{
		ProfileID: profile.ProfileID, ProfileVersion: profile.version, Release: release,
		ExpectedRevision: value, CollectedAt: now(),
		Limitations: []string{"The build identity is service-reported provenance, not a signed attestation."},
	}
	if value != "" {
		matches := value == release.Revision
		response.RevisionMatches = &matches
	}
	return response, nil
}

func readConfiguration(ctx context.Context, profile serviceProfile, client evidenceClient, now func() time.Time) (runtimeConfigurationResponse, error) {
	before, err := client.getBuildInfo(ctx, profile)
	if err != nil {
		return runtimeConfigurationResponse{}, err
	}
	products, err := client.getCatalog(ctx, profile)
	if err != nil {
		return runtimeConfigurationResponse{}, err
	}
	after, err := client.getBuildInfo(ctx, profile)
	if err != nil {
		return runtimeConfigurationResponse{}, err
	}
	if before.Revision != after.Revision {
		return runtimeConfigurationResponse{}, errReleaseChanged
	}
	return runtimeConfigurationResponse{
		ProfileID: profile.ProfileID, ProfileVersion: profile.version,
		ConfigurationType: "retail-catalog", ServingRevision: before.Revision,
		Configuration: runtimeConfiguration{Products: products}, CollectedAt: now(),
		Applicability: "current-release-only",
		Limitations:   []string{"Current configuration is not automatically applicable to historical observations."},
	}, nil
}
