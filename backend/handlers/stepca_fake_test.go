package handlers

import (
	"context"

	"step-ui/stepca"
)

// FakeCA is a test double for stepca.CA — the direct replacement for the
// role makeFakeRunner/fakeRunnerResult played before this migration (see
// cert_backup_test.go, deleted per Phase 6.1). Zero value is a CA that
// succeeds every call with empty/zero results.
type FakeCA struct {
	HealthErr error

	ProvisionersResult []stepca.ProvisionerInfo
	ProvisionersErr    error

	IssueResult struct {
		Cert []byte
		Key  []byte
	}
	IssueErr error

	RevokeErr error
}

var _ stepca.CA = (*FakeCA)(nil)

func (f *FakeCA) Health(context.Context) error {
	return f.HealthErr
}

func (f *FakeCA) Provisioners(context.Context) ([]stepca.ProvisionerInfo, error) {
	return f.ProvisionersResult, f.ProvisionersErr
}

func (f *FakeCA) IssueCertificate(context.Context, stepca.IssueRequest) ([]byte, []byte, error) {
	return f.IssueResult.Cert, f.IssueResult.Key, f.IssueErr
}

func (f *FakeCA) Revoke(context.Context, string, string) error {
	return f.RevokeErr
}
