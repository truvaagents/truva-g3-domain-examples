package main

import "context"

// Historical attribution uses the receipt's original identity. Current caller
// authorization is enforced by the handler; current repository scope remains
// mandatory here. This read grants no authority to publish under an old profile.
// get_publication and publication mutation validation retain their current pins.
func (operations *publicationOperations) remediationPublication(ctx context.Context, profile publicationProfile, input remediationRequest) (publicationRecord, error) {
	record, err := operations.store.get(ctx, input.PublicationID)
	if err != nil {
		return publicationRecord{}, err
	}
	if record.SchemaVersion != 1 || record.PublicationID != input.PublicationID ||
		record.ProfileID != profile.PublicationProfileID || !sha256Pattern.MatchString(record.ProfileSHA256) ||
		record.CaseID != input.OriginCaseID || record.RepositoryID != profile.RepositoryID ||
		record.RepositoryOwner != profile.Owner || record.RepositoryName != profile.Repository ||
		!candidateIDPattern.MatchString(record.CandidateID) || !verificationIDPattern.MatchString(record.VerificationID) ||
		!reviewIDPattern.MatchString(record.ReviewID) || !publicationNamePattern.MatchString(record.WorkspaceProfileID) ||
		!record.ExpiresAt.After(operations.now()) {
		return publicationRecord{}, failPublication("record_invalid", false, nil)
	}
	originalProfile := publicationProfile{PublicationProfileID: record.ProfileID, profileSHA256: record.ProfileSHA256}
	identity := publishRequest{CaseID: record.CaseID, CandidateID: record.CandidateID, VerificationID: record.VerificationID, ReviewID: record.ReviewID}
	if record.PublicationID != publicationIdentity(originalProfile, identity) {
		return publicationRecord{}, failPublication("record_invalid", false, nil)
	}
	return record, nil
}
