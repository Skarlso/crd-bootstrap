package configmap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	crdKey = "crd.yaml"
	//version = "version"
)

// Source defines a source that can fetch CRD data from a config map.
type Source struct {
	client client.Client
	next   source.Contract
}

// NewSource creates a new ConfigMap handling Source.
func NewSource(client client.Client, next source.Contract) *Source {
	return &Source{client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, source v1alpha1.Source) (string, error) {
	if source.ConfigMap == nil {
		if s.next == nil {
			return "", fmt.Errorf("configmap isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, source)
	}

	configMap := &v1.ConfigMap{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      source.ConfigMap.Name,
		Namespace: source.ConfigMap.Namespace,
	}, configMap); err != nil {
		return "", fmt.Errorf("failed to find config map: %w", err)
	}

	content, ok := configMap.Data[crdKey]
	if !ok {
		return "", fmt.Errorf("failed to find '%s' in config map", crdKey)
	}

	file := filepath.Join(dir, "crd.yaml")
	if err := os.WriteFile(file, []byte(content), 0o777); err != nil {
		return "", fmt.Errorf("failed to create crd file from config map: %w", err)
	}

	return file, nil
}
