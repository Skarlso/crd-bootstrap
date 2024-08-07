package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
)

const (
	gitlabAPIBase = "https://gitlab.com/api/v4"
)

// Source provides functionality to fetch a CRD yaml from a gitlab release.
type Source struct {
	Client *http.Client

	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new gitlab handling Source.
func NewSource(c *http.Client, client client.Client, next source.Contract) *Source {
	return &Source{Client: c, client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error) {
	if obj.Spec.Source.GitLab == nil {
		if s.next == nil {
			return "", errors.New("gitlab isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	if err := s.fetch(ctx, revision, dir, obj); err != nil {
		return "", fmt.Errorf("failed to fetch CRD: %w", err)
	}

	return filepath.Join(dir, obj.Spec.Source.GitLab.Manifest), nil
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	if obj.Spec.Source.GitLab == nil {
		if s.next == nil {
			return false, "", errors.New("gitlab isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	latestVersion, err := s.getLatestVersion(ctx, obj)
	if err != nil {
		return false, "", fmt.Errorf("failed to retrieve latest version for gitlab: %w", err)
	}

	latestVersionSemver, err := semver.NewVersion(latestVersion)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse current version '%s' as semver: %w", latestVersion, err)
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
		return true, latestVersion, nil
	}

	return false, obj.Status.LastAppliedRevision, nil
}

// getLatestVersion calls the gitlab API and returns the latest released version.
func (s *Source) getLatestVersion(ctx context.Context, obj *v1alpha1.Bootstrap) (string, error) {
	logger := log.FromContext(ctx)
	c := s.Client
	if obj.Spec.Source.GitLab.SecretRef != nil {
		var err error
		c, err = s.constructAuthenticatedClient(ctx, obj)
		if err != nil {
			return "", fmt.Errorf("failed to construct authenticated client: %w", err)
		}
	}

	c.Timeout = 15 * time.Second

	baseAPIURL := obj.Spec.Source.GitLab.BaseAPIURL
	if baseAPIURL == "" {
		baseAPIURL = gitlabAPIBase
	}

	// https://gitlab.com/api/v4/projects/52955411/releases/permalink/latest
	// https://gitlab.com/api/v4/projects/skarlso%2Fgitlab-test-1/releases/permalink/latest
	latestURL := fmt.Sprintf("%s/projects/%s%s%s/releases/permalink/latest", baseAPIURL, obj.Spec.Source.GitLab.Owner, "%2F", obj.Spec.Source.GitLab.Repo)
	logger.Info("checking for latest version under url", "url", latestURL)

	body, err := s.fetchURLContent(ctx, c, latestURL)
	// immediately check even in case of error.
	if body != nil {
		defer body.Close()
	}

	if err != nil {
		return "", fmt.Errorf("failed to read url content: %w", err)
	}

	type meta struct {
		Tag string `json:"tag_name"`
	}
	var m meta
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		return "", fmt.Errorf("decoding gitlab API response failed: %w", err)
	}

	if m.Tag == "" {
		return "", errors.New("failed to retrieve latest version, please make sure owner and repo are spelled correctly")
	}

	logger.Info("latest version found", "version", m.Tag)

	return m.Tag, err
}

// fetch fetches the content.
func (s *Source) fetch(ctx context.Context, version, dir string, obj *v1alpha1.Bootstrap) error {
	baseAPIURL := obj.Spec.Source.GitLab.BaseAPIURL
	if baseAPIURL == "" {
		baseAPIURL = gitlabAPIBase
	}

	// construct client
	var err error
	client := s.Client
	if obj.Spec.Source.GitLab.SecretRef != nil {
		client, err = s.constructAuthenticatedClient(ctx, obj)
		if err != nil {
			return fmt.Errorf("failed to construct authenticated client: %w", err)
		}
	}

	downloadURL := fmt.Sprintf("%s/projects/%s%s%s/releases/%s", baseAPIURL, obj.Spec.Source.GitLab.Owner, "%2F", obj.Spec.Source.GitLab.Repo, version)
	body, err := s.fetchURLContent(ctx, client, downloadURL)
	// immediately check even in case of error.
	if body != nil {
		defer body.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to download url content: %w", err)
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("failed to read full body: %w", err)
	}

	type meta struct {
		Assets struct {
			Links []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"links"`
		} `json:"assets"`
	}
	var assets meta
	if err := json.Unmarshal(content, &assets); err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	var assetURL string
	for _, a := range assets.Assets.Links {
		if a.Name == obj.Spec.Source.GitLab.Manifest {
			assetURL = a.URL

			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("asset link not found under release assets in release with name %s", obj.Spec.Source.GitLab.Manifest)
	}

	assetBody, err := s.fetchURLContent(ctx, client, assetURL)
	// immediately check even in case of error.
	if assetBody != nil {
		defer assetBody.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to download url content: %w", err)
	}

	wf, err := os.Create(filepath.Join(dir, obj.Spec.Source.GitLab.Manifest))
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	defer wf.Close()

	// stream the asset content into a temp file
	if _, err := io.Copy(wf, assetBody); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	return nil
}

// fetchURLContent return the body as a reader so the caller can stream it.
func (s *Source) fetchURLContent(ctx context.Context, c *http.Client, url string) (io.ReadCloser, error) {
	logger := log.FromContext(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab API call failed: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		content, err := io.ReadAll(res.Body)
		if err != nil {
			logger.Error(errors.New("failed to read body for further information"), "failed to read body for further information")
		}

		logger.Error(fmt.Errorf("unexpected status code from gitlab (%d)", res.StatusCode), "unexpected status code from gitlab with message", "message", string(content))

		return nil, fmt.Errorf("gitlab API returned an unexpected status code (%d)", res.StatusCode)
	}

	return res.Body, nil
}

func (s *Source) constructAuthenticatedClient(ctx context.Context, obj *v1alpha1.Bootstrap) (*http.Client, error) {
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: obj.Spec.Source.GitLab.SecretRef.Name, Namespace: obj.Namespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to find secret ref for token: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return nil, errors.New("token key not found in provided secret")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: string(token)},
	)

	return oauth2.NewClient(ctx, ts), nil
}
