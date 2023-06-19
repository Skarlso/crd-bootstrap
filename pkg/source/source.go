package source

import (
	"context"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

// Contract defines the capabilities of a source provider.
type Contract interface {
	FetchCRD(ctx context.Context, dir string, source v1alpha1.Source) (string, error)
}
