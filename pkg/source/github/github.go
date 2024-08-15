package github

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
	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
	"github.com/Skarlso/crd-bootstrap/pkg/source/auth"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	githubBase    = "https://github.com"
	githubAPIBase = "https://api.github.com"
)

// Source provides functionality to fetch a CRD yaml from a GitHub release.
type Source struct {
	Client *http.Client

	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new GitHub handling Source.
func NewSource(c *http.Client, client client.Client, next source.Contract) *Source {
	return &Source{Client: c, client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error) {
	if obj.Spec.Source.GitHub == nil {
		if s.next == nil {
			return "", errors.New("github isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	if err := s.fetch(ctx, revision, dir, obj); err != nil {
		return "", fmt.Errorf("failed to fetch CRD: %w", err)
	}

	return filepath.Join(dir, obj.Spec.Source.GitHub.Manifest), nil
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	if obj.Spec.Source.GitHub == nil {
		if s.next == nil {
			return false, "", errors.New("github isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	latestVersion, err := s.getLatestVersion(ctx, obj)
	if err != nil {
		return false, "", fmt.Errorf("failed to retrieve latest version for github: %w", err)
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

// getLatestVersion calls the GitHub API and returns the latest released version.
func (s *Source) getLatestVersion(ctx context.Context, obj *v1alpha1.Bootstrap) (string, error) {
	logger := log.FromContext(ctx)
	c := s.Client
	if obj.Spec.Source.GitHub.SecretRef != nil {
		var err error
		c, err = auth.ConstructAuthenticatedClient(ctx, s.client, obj.Spec.Source.GitHub.SecretRef.Name, obj.Namespace)
		if err != nil {
			return "", fmt.Errorf("failed to construct authenticated client: %w", err)
		}
	}

	c.Timeout = 15 * time.Second

	baseAPIURL := obj.Spec.Source.GitHub.BaseAPIURL
	if baseAPIURL == "" {
		baseAPIURL = githubAPIBase
	}

	latestURL := fmt.Sprintf("%s/repos/%s/%s/releases/latest", baseAPIURL, obj.Spec.Source.GitHub.Owner, obj.Spec.Source.GitHub.Repo)
	logger.Info("checking for latest version under url", "url", latestURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	res, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API call failed: %w", err)
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		content, err := io.ReadAll(res.Body)
		if err != nil {
			logger.Error(errors.New("failed to read body for further information"), "failed to read body for further information")
		}

		logger.Error(fmt.Errorf("unexpected status code from github (%d)", res.StatusCode), "unexpected status code from github with message", "message", string(content))

		return "", fmt.Errorf("GitHub API returned an unexpected status code (%d)", res.StatusCode)
	}

	type meta struct {
		Tag string `json:"tag_name"`
	}
	var m meta
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("decoding GitHub API response failed: %w", err)
	}

	if m.Tag == "" {
		return "", errors.New("failed to retrieve latest version, please make sure owner and repo are spelled correctly")
	}

	return m.Tag, err
}

// fetch fetches the content.
func (s *Source) fetch(ctx context.Context, version, dir string, obj *v1alpha1.Bootstrap) error {
	baseURL := obj.Spec.Source.GitHub.BaseURL
	if baseURL == "" {
		baseURL = githubBase
	}

	baseURL = fmt.Sprintf("%s/%s/%s/releases", baseURL, obj.Spec.Source.GitHub.Owner, obj.Spec.Source.GitHub.Repo)
	downloadURL := fmt.Sprintf("%s/download/%s/%s", baseURL, version, obj.Spec.Source.GitHub.Manifest)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request for %s, error: %w", downloadURL, err)
	}

	// download
	client := s.Client
	if obj.Spec.Source.GitHub.SecretRef != nil {
		client, err = auth.ConstructAuthenticatedClient(ctx, s.client, obj.Spec.Source.GitHub.SecretRef.Name, obj.Namespace)
		if err != nil {
			return fmt.Errorf("failed to construct authenticated client: %w", err)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download %s from %s, error: %w", obj.Spec.Source.GitHub.Manifest, downloadURL, err)
	}
	defer resp.Body.Close()

	// check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s from %s, status: %s", obj.Spec.Source.GitHub.Manifest, downloadURL, resp.Status)
	}

	wf, err := os.Create(filepath.Join(dir, obj.Spec.Source.GitHub.Manifest))
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	defer wf.Close()

	if _, err := io.Copy(wf, resp.Body); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	return nil
}
