package helm

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"k8s.io/apimachinery/pkg/util/yaml"
	"oras.land/oras-go/pkg/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
)

type Source struct {
	Client *http.Client

	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new Helm handling Source.
func NewSource(c *http.Client, client client.Client, next source.Contract) *Source {
	return &Source{Client: c, client: client, next: next}
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
		registry.ClientOptPlainHTTP(), // this needs to be configurable with cred files
	}

	// Create a new registry client
	registryClient, err := registry.NewClient(opts...)
	if err != nil {
		return "", fmt.Errorf("failed to create registry: %w", err)
	}

	tempHelm := filepath.Join(dir, "helm-temp")
	if err := os.MkdirAll(tempHelm, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp helm folder: %w", err)
	}

	defer os.Remove(tempHelm)

	client := action.NewPullWithOpts(action.WithConfig(new(action.Configuration)))
	client.Version = revision

	client.DestDir = tempHelm
	client.Settings = &cli.EnvSettings{}
	client.SetRegistryClient(registryClient)
	if registry.IsOCI(obj.Spec.Source.Helm.ChartReference) {
		client.Untar = true
	}

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

type entry struct {
	Version string `yaml:"version"`
}

// results parses the index file for https helm repos to get latest versions
// doing this because helm's search requires a lot of work and fiddling
// by adding repos first, then updating them, THEN run search.
// In case of a large index file this might get tricky.
type results struct {
	APIVersion string             `yaml:"apiVersion"`
	Entries    map[string][]entry `yaml:"entries"`
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	if obj.Spec.Source.Helm == nil {
		if s.next == nil {
			return false, "", fmt.Errorf("helm isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	var (
		versions []string
		err      error
	)
	if registry.IsOCI(obj.Spec.Source.Helm.ChartReference) {
		versions, err = s.findVersionsForOCIRegistry(obj.Spec.Source.Helm.ChartReference)
		if err != nil {
			return false, "", err
		}
	} else {
		versions, err = s.findVersionsForHTTPRepository(ctx, obj.Spec.Source.Helm.ChartReference, obj.Spec.Source.Helm.ChartName)
		if err != nil {
			return false, "", err
		}
	}

	// get latest version that applies to the constraint.
	constrains, err := semver.NewConstraint(obj.Spec.Version.Semver)
	if err != nil {
		return false, "", fmt.Errorf("failed to build constraint: %w", err)
	}

	latestRemoteVersion := s.getLatestVersion(versions, constrains)
	latestVersionSemver, err := semver.NewVersion(latestRemoteVersion)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse current version '%s' as semver: %w", latestRemoteVersion, err)
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
				return false, "", fmt.Errorf("failed to parse last applied revision '%s': %w", obj.Status.LastAppliedRevision, err)
			}

			if lastAppliedRevisionSemver.Equal(latestVersionSemver) || lastAppliedRevisionSemver.GreaterThan(latestVersionSemver) {
				return false, obj.Status.LastAppliedRevision, nil
			}
		}

		// last applied revision was either empty, or lower than the last version that satisfied the constraint.
		// return update needed and the latest fetched version.
		return true, latestRemoteVersion, nil
	}

	return false, obj.Status.LastAppliedRevision, nil
}

// getLatestVersion selects all the versions that match the constraint and gets back the latest.
func (s *Source) getLatestVersion(versions []string, constraint *semver.Constraints) string {
	semvers := make([]*semver.Version, 0)
	for _, v := range versions {
		semv, err := semver.NewVersion(v)
		if err != nil {
			// log and continue
			continue
		}

		if constraint.Check(semv) {
			semvers = append(semvers, semv)
		}
	}

	sort.Slice(semvers, func(i, j int) bool {
		return semvers[i].GreaterThan(semvers[j])
	})

	return semvers[0].Original()
}

func (s *Source) findVersionsForOCIRegistry(chartRef string) ([]string, error) {
	var versions []string
	// helm's own way of doing this just doesn't work.
	src, err := remote.NewRepository(strings.TrimPrefix(chartRef, "oci://"))
	if err != nil {
		return nil, fmt.Errorf("failed to construct repository: %w", err)
	}
	if err := src.Tags(context.Background(), func(tags []string) error {
		versions = append(versions, tags...)

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to fetch tags: %w", err)
	}

	return versions, nil
}

func (s *Source) findVersionsForHTTPRepository(ctx context.Context, chartRef, chartName string) ([]string, error) {
	u, err := url.JoinPath(chartRef, "index.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to join path: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct request: %w", err)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("status code returned is invalid %d", resp.StatusCode)
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// leaving dir empty will create a temp dir
	tempFile, err := os.CreateTemp("", "index.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for response: %w", err)
	}

	defer tempFile.Close()

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return nil, fmt.Errorf("failed to copy content to file: %w", err)
	}

	// NOTE: This can be improved with a streaming reader if the need really arises.
	content, err := os.ReadFile(tempFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to read downloaded file: %w", err)
	}

	res := &results{}
	if err := yaml.Unmarshal(content, &res); err != nil {
		return nil, err
	}

	v, ok := res.Entries[chartName]
	if !ok {
		return nil, fmt.Errorf("no charts found in registry with name %s", chartName)
	}

	versions := make([]string, 0, len(v))
	for _, e := range v {
		versions = append(versions, e.Version)
	}

	return versions, nil
}
