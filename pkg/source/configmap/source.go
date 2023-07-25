package configmap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
)

var (
	crdKey  = "crd.yaml"
	version = "version"
)

// Source defines a source that can fetch CRD data from a config map.
type Source struct {
	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new ConfigMap handling Source.
func NewSource(client client.Client, next source.Contract) *Source {
	return &Source{client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error) {
	if obj.Spec.Source.ConfigMap == nil {
		if s.next == nil {
			return "", fmt.Errorf("configmap isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	configMap := &v1.ConfigMap{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      obj.Spec.Source.ConfigMap.Name,
		Namespace: obj.Spec.Source.ConfigMap.Namespace,
	}, configMap); err != nil {
		return "", fmt.Errorf("failed to find config map: %w", err)
	}

	v, ok := configMap.Data[version]
	if !ok {
		return "", fmt.Errorf("version key not defined in configmap")
	}

	if v != revision {
		return "", fmt.Errorf("fetched revision '%s' does not equal requested '%s'", v, revision)
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

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	if obj.Spec.Source.ConfigMap == nil {
		if s.next == nil {
			return false, "", fmt.Errorf("configmap isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	configMap := &v1.ConfigMap{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      obj.Spec.Source.ConfigMap.Name,
		Namespace: obj.Spec.Source.ConfigMap.Namespace,
	}, configMap); err != nil {
		return false, "", fmt.Errorf("failed to find config map: %w", err)
	}

	latestVersion, ok := configMap.Data[version]
	if !ok {
		return false, "", fmt.Errorf("version key not defined in configmap")
	}
	latestVersionSemver, err := semver.NewVersion(latestVersion)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse current config map version '%s' as semver: %w", latestVersion, err)
	}

	constraint, err := semver.NewConstraint(obj.Spec.Version.Semver)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse constraint: %w", err)
	}

	// If the latest version satisfies the constraint, we check it against the latest applied version if it's set.
	if constraint.Check(latestVersionSemver) {
		if obj.Status.LastAppliedRevision != "" {
			// we know this could be a digest, we don't allow switching forms in a bootstrap.
			// i.e.: configmap was used as a source, but we switched to URL instead.
			lastAppliedRevisionSemver, err := semver.NewVersion(obj.Status.LastAppliedRevision)
			if err != nil {
				return false, "", fmt.Errorf("failed to parse last applied revision '%s'; expected version for config map source: %w", obj.Status.LastAppliedRevision, err)
			}

			if lastAppliedRevisionSemver.Equal(latestVersionSemver) || lastAppliedRevisionSemver.GreaterThan(latestVersionSemver) {
				return false, obj.Status.LastAppliedRevision, nil
			}
		}

		// last applied revision was either empty, or lower than the last version that satisfied the constraint.
		// return update needed and the latest fetched version.
		return true, latestVersion, nil
	}

	return false, obj.Status.LastAppliedRevision, nil
}
