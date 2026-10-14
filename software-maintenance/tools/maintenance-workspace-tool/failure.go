package main

type workspaceServiceFailure struct {
	retryable bool
	cause     error
}

func (failure *workspaceServiceFailure) Error() string {
	return "workspace dependency or storage failure"
}

func (failure *workspaceServiceFailure) Unwrap() error {
	return failure.cause
}

func serviceFailure(cause error, retryable bool) error {
	return &workspaceServiceFailure{retryable: retryable, cause: cause}
}
