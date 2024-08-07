package helm

import (
	"context"
	"errors"
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
	"github.com/docker/cli/cli/config/configfile"
	"golang.org/x/oauth2"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"oras.land/oras-go/pkg/registry/remote"
	"oras.land/oras-go/pkg/registry/remote/auth"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	if obj.Spec.Source.Helm == nil {
		if s.next == nil {
			return "", errors.New("helm isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	var out strings.Builder
	var options []getter.Option
	download := &downloader.ChartDownloader{
		Out:     &out,
		Verify:  downloader.VerifyNever,
		Getters: getter.All(&cli.EnvSettings{}),
		Options: options,
	}

	if obj.Spec.Source.Helm.SecretRef != nil {
		if err := s.configureCredentials(ctx, obj, download); err != nil {
			return "", err
		}
	}

	tempHelm := filepath.Join(dir, "helm-temp")
	if err := os.MkdirAll(tempHelm, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp helm folder: %w", err)
	}
	defer os.RemoveAll(tempHelm)

	outputPath, _, err := download.DownloadTo(obj.Spec.Source.Helm.ChartReference, revision, tempHelm)
	if err != nil {
		return "", fmt.Errorf("failed to download chart: %w", err)
	}

	if registry.IsOCI(obj.Spec.Source.Helm.ChartReference) {
		if err := chartutil.ExpandFile(tempHelm, outputPath); err != nil {
			return "", fmt.Errorf("failed ot untar: %w", err)
		}
	}

	if err := s.createCrdYaml(dir, tempHelm); err != nil {
		return "", fmt.Errorf("failed to create crd yaml: %w", err)
	}

	return filepath.Join(dir, "crds.yaml"), nil
}

func (s *Source) configureCredentials(ctx context.Context, obj *v1alpha1.Bootstrap, download *downloader.ChartDownloader) error {
	secret := &v1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: obj.Spec.Source.Helm.SecretRef.Name, Namespace: obj.Namespace}, secret); err != nil {
		return fmt.Errorf("failed to find attached secret: %w", err)
	}

	if registry.IsOCI(obj.Spec.Source.Helm.ChartReference) {
		if err := s.configureOCICredentials(secret, obj.Spec.Source.Helm.ChartReference, download); err != nil {
			return fmt.Errorf("failed to configure oci repository: %w", err)
		}
	} else {
		password, ok := secret.Data[v1alpha1.PasswordKey]
		if !ok {
			return errors.New("missing password key")
		}
		username, ok := secret.Data[v1alpha1.UsernameKey]
		if !ok {
			return errors.New("missing username key")
		}

		download.Options = append(download.Options,
			getter.WithBasicAuth(string(username), string(password)),
			getter.WithPassCredentialsAll(true),
		)
	}

	return nil
}

func (s *Source) createCrdYaml(dir string, tempHelm string) error {
	crds, err := os.Create(filepath.Join(dir, "crds.yaml"))
	if err != nil {
		return fmt.Errorf("failed to create crds bundle file: %w", err)
	}
	defer crds.Close()

	// find all yaml files that contain CRDs in them and append to the end result.
	if err := filepath.Walk(tempHelm, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

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
		return fmt.Errorf("failed to walk path: %w", err)
	}

	return nil
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
			return false, "", errors.New("helm isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	var (
		versions []string
		err      error
	)
	if registry.IsOCI(obj.Spec.Source.Helm.ChartReference) {
		versions, err = s.findVersionsForOCIRegistry(ctx, obj.Spec.Source.Helm, obj.Namespace)
		if err != nil {
			return false, "", err
		}
	} else {
		versions, err = s.findVersionsForHTTPRepository(ctx, obj.Spec.Source.Helm, obj.Namespace)
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

func (s *Source) findVersionsForOCIRegistry(ctx context.Context, chartRef *v1alpha1.Helm, namespace string) ([]string, error) {
	var versions []string
	// helm's own way of doing this just doesn't work.
	src, err := remote.NewRepository(strings.TrimPrefix(chartRef.ChartReference, "oci://"))
	if err != nil {
		return nil, fmt.Errorf("failed to construct repository: %w", err)
	}
	if chartRef.SecretRef != nil {
		if err := s.configureTransportForOCIRepo(ctx, src, chartRef.SecretRef, namespace); err != nil {
			return nil, fmt.Errorf("failed to configure transport client: %w", err)
		}
	}
	if err := src.Tags(context.Background(), func(tags []string) error {
		versions = append(versions, tags...)

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to fetch tags: %w", err)
	}

	return versions, nil
}

func (s *Source) findVersionsForHTTPRepository(ctx context.Context, chartRef *v1alpha1.Helm, namespace string) ([]string, error) {
	u, err := url.JoinPath(chartRef.ChartReference, "index.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to join path: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct request: %w", err)
	}

	innerClient := s.Client

	if chartRef.SecretRef != nil {
		secret := &v1.Secret{}
		if err := s.client.Get(ctx, types.NamespacedName{Name: chartRef.SecretRef.Name, Namespace: namespace}, secret); err != nil {
			return nil, fmt.Errorf("failed to find attached secret: %w", err)
		}

		innerClient, err = s.configureHTTPCredentials(ctx, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to configure secure access to HTTP repo: %w", err)
		}
	}

	resp, err := innerClient.Do(req)
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

	v, ok := res.Entries[chartRef.ChartName]
	if !ok {
		return nil, fmt.Errorf("no charts found in registry with name %s", chartRef.ChartName)
	}

	versions := make([]string, 0, len(v))
	for _, e := range v {
		versions = append(versions, e.Version)
	}

	return versions, nil
}

func (s *Source) configureTransportForOCIRepo(ctx context.Context, src *remote.Repository, ref *v1.LocalObjectReference, namespace string) error {
	secret := &v1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, secret); err != nil {
		return fmt.Errorf("failed to find attached secret: %w", err)
	}
	config, ok := secret.Data[v1alpha1.DockerJSONConfigKey]
	if !ok {
		return errors.New("password wasn't defined in given secret")
	}
	tmpConfig, err := os.CreateTemp("", "config.json")
	if err != nil {
		return fmt.Errorf("failed to create a temp config: %w", err)
	}
	defer os.Remove(tmpConfig.Name())

	host := src.Reference.Host()
	conf := configfile.New(tmpConfig.Name())
	if err := conf.LoadFromReader(strings.NewReader(string(config))); err != nil {
		return fmt.Errorf("failed to parse the config: %w", err)
	}
	authForHost, ok := conf.AuthConfigs[host]
	if !ok {
		return fmt.Errorf("failed to find auth configuration for host %s", host)
	}

	c := &auth.Client{
		Credential: func(_ context.Context, _ string) (auth.Credential, error) {
			return auth.Credential{
				Username: authForHost.Username,
				Password: authForHost.Password,
			}, nil
		},
	}

	src.Client = c

	return nil
}

func (s *Source) configureOCICredentials(secret *v1.Secret, ref string, download *downloader.ChartDownloader) error {
	config, ok := secret.Data[v1alpha1.DockerJSONConfigKey]
	if !ok {
		return errors.New("dockerjsonconfig is needed in secret to access OCI repository")
	}

	tmpConfig, err := os.CreateTemp("", "config.json")
	if err != nil {
		return fmt.Errorf("failed to create a temp config: %w", err)
	}

	defer os.Remove(tmpConfig.Name())
	src, err := remote.NewRepository(strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		return fmt.Errorf("failed to construct repository: %w", err)
	}

	host := src.Reference.Host()
	conf := configfile.New(tmpConfig.Name())
	if err := conf.LoadFromReader(strings.NewReader(string(config))); err != nil {
		return fmt.Errorf("failed to parse the config: %w", err)
	}
	if err := conf.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	authForHost, ok := conf.AuthConfigs[host]
	if !ok {
		return fmt.Errorf("failed to find auth configuration for host %s", host)
	}

	download.Options = append(download.Options,
		getter.WithBasicAuth(authForHost.Username, authForHost.Password),
		getter.WithPassCredentialsAll(true),
	)

	// write out the docker config and pass it in
	opts := []registry.ClientOption{
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stderr),
		registry.ClientOptCredentialsFile(tmpConfig.Name()),
	}

	registryClient, err := registry.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("failed to create registry: %w", err)
	}

	download.Options = append(download.Options,
		getter.WithRegistryClient(registryClient),
		getter.WithUntar(),
	)
	download.RegistryClient = registryClient

	return nil
}

func (s *Source) configureHTTPCredentials(ctx context.Context, secret *v1.Secret) (*http.Client, error) {
	token, ok := secret.Data[v1alpha1.PasswordKey]
	if !ok {
		return nil, errors.New("missing password key")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: string(token)},
	)

	return oauth2.NewClient(ctx, ts), nil
}
