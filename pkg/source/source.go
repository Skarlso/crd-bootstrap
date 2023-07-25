package source

import (
	"context"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

// Contract defines the capabilities of a source provider.
type Contract interface {
	// FetchCRD fetches the latest CRD if there is an update available.
	// The returned thing is the location to the CRD. This function should not return the CRD content
	// as it could be several megabytes large.
	FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error)
	// HasUpdate returns true and the version if there is an update available.
	// In case of a URL this would be the digest. This logic follows this general guide:
	// - Fetch latest version that satisfies the constraint
	// - Compare to last applied revision
	// - Return true and the version if there is something to apply
	// - Return false and empty string if there is nothing to apply.
	HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error)
}
