package helm

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
)

type Source struct {
	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new Helm handling Source.
func NewSource(client client.Client, next source.Contract) *Source {
	return &Source{client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error) {
	logger := log.FromContext(ctx)

	if obj.Spec.Source.Helm == nil {
		if s.next == nil {
			return "", fmt.Errorf("helm isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	opts := []registry.ClientOption{
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stderr),
		registry.ClientOptPlainHTTP(),
	}

	// Create a new registry client
	registryClient, err := registry.NewClient(opts...)
	if err != nil {
		return "", fmt.Errorf("failed to create registry: %w", err)
	}

	tempHelm := filepath.Join(dir, "temp")
	if err := os.MkdirAll(tempHelm, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp helm folder: %w", err)
	}

	defer os.Remove(tempHelm)

	client := action.NewPullWithOpts(action.WithConfig(new(action.Configuration)))
	client.Version = revision
	client.Untar = true
	client.DestDir = tempHelm
	client.Settings = &cli.EnvSettings{}
	client.SetRegistryClient(registryClient)

	output, err := client.Run(obj.Spec.Source.Helm.ChartReference)
	if err != nil {
		logger.V(4).Info("got output from helm downloader", "output", output)

		return "", fmt.Errorf("failed to download helm chart: %w", err)
	}

	crds, err := os.Create(filepath.Join(dir, "crds.yaml"))
	if err != nil {
		return "", fmt.Errorf("failed to create crds bundle file: %w", err)
	}
	defer crds.Close()

	// find all yaml files that contain CRDs in them and append to the end result.
	if err := filepath.Walk(tempHelm, func(path string, info fs.FileInfo, err error) error {
		if info.Name() == "crds" && info.IsDir() {
			files, err := os.ReadDir(path)
			if err != nil {
				return fmt.Errorf("failed to read CRDs folder: %w", err)
			}

			for _, f := range files {
				content, err := os.ReadFile(filepath.Join(path, f.Name()))
				if err != nil {
					return fmt.Errorf("failed to read file %s: %w", filepath.Join(path, f.Name()), err)
				}

				_, _ = crds.WriteString("---\n")
				_, _ = crds.Write(content)
			}
		}

		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to walk path: %w", err)
	}

	return filepath.Join(dir, "crds.yaml"), nil
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	return true, obj.Spec.Version.Semver, nil
}
