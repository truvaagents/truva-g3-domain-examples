package main

type publicationFailure struct {
	kind      string
	retryable bool
	cause     error
	github    *githubFailureEvidence
}

func (failure *publicationFailure) Error() string {
	return "publication operation failed: " + failure.kind
}

func (failure *publicationFailure) Unwrap() error {
	return failure.cause
}

func failPublication(kind string, retryable bool, cause error) error {
	return &publicationFailure{kind: kind, retryable: retryable, cause: cause}
}
