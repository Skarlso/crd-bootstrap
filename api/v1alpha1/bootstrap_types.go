/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"time"

	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BootstrapOwnerLabelKey = "delivery.crd-bootstrap.owned"
)

// GitHub defines a GitHub type source where the CRD is coming from `release` section of a GitHub repository.
type GitHub struct {
	// BaseURL is used for the GitHub url. Defaults to github.com if not defined.
	// +optional
	BaseURL string `json:"baseURL,omitempty"`
	// BaseAPIURL is used for the GitHub API url. Defaults to api.github.com if not defined.
	// +optional
	BaseAPIURL string `json:"baseAPIURL,omitempty"`

	// Owner defines the owner of the repository.
	// +required
	Owner string `json:"owner"`

	// Repo defines the name of the repository.
	// +required
	Repo string `json:"repo"`

	// SecretRef contains a pointed to a Token in case the repository is private.
	// +optional
	SecretRef *v1.LocalObjectReference `json:"secretRef,omitempty"`
	// Manifest defines the name of the manifest that contains the CRD definitions on the GitHub release page.
	// +required
	Manifest string `json:"manifest"`
}

// ConfigMap defines a reference to a configmap which hold the CRD information. Version is taken from a version field.
type ConfigMap struct {
	// Name of the config map.
	// +required
	Name string `json:"name"`
	// Namespace of the config map.
	// +required
	Namespace string `json:"namespace"`
}

// URL holds a URL from which to fetch the CRD. Version is defined through the digest of the content.
type URL struct {
	// URL defines the URL from which do download the YAML content from.
	// +required
	URL string `json:"url"`
	// SecretRef contains a pointed to a Token in case the URL isn't public.
	// +optional
	SecretRef *v1.LocalObjectReference `json:"secretRef,omitempty"`
}

// Source defines options from where to fetch CRD content.
type Source struct {
	// GitHub type source.
	// +optional
	GitHub *GitHub `json:"gitHub,omitempty"`
	// ConfigMap type source.
	// +optional
	ConfigMap *ConfigMap `json:"configMap,omitempty"`
	// URL type source.
	// +optional
	URL *URL `json:"url,omitempty"`
}

// Version defines options to look at when trying to determine what version is allowed to be fetched / applied.
type Version struct {
	// Semver defines a possible constraint like `>=v1`.
	// +optional
	Semver string `json:"semver,omitempty"`

	// Digest defines the digest of the content pointing to a URL.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// BootstrapSpec defines the desired state of Bootstrap.
type BootstrapSpec struct {
	// Interval defines the regular interval at which a poll for new version should happen.
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`

	// Source defines a reference to a source which will provide a CRD based on some contract.
	// +required
	Source *Source `json:"source"`

	// Version defines constraints for sources to check against. It can either be a semver constraint or a Digest
	// in case of URLs. If a digest is defined, URL sync will ONLY SYNC that digest. If the digest
	// differs, it will NOT install it.
	// +optional
	Version Version `json:"version,omitempty"`

	// Template defines a set of values to test a new version against.
	// +optional
	Template map[string]*apiextensionsv1.JSON `json:"template,omitempty"`

	// ContinueOnValidationError will still apply a CRD even if the validation failed for it.
	// +optional
	ContinueOnValidationError bool `json:"continueOnValidationError,omitempty"`

	// Prune will clean up all applied objects once the Bootstrap object is removed.
	// +optional
	Prune bool `json:"prune,omitempty"`
}

// BootstrapStatus defines the observed state of Bootstrap.
type BootstrapStatus struct {
	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions contains the conditions of this object.
	// +optional
	// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
	// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastAppliedCRDNames contains the names of the last applied CRDs and the number of times they were applied.
	// +optional
	LastAppliedCRDNames map[string]int `json:"lastAppliedCRDNames,omitempty"`

	// LastAttemptedRevision contains the version or the digest that was tried to be applied and was either successful or failed.
	// +optional
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`

	// LastAppliedRevision version is the version or the digest that was successfully applied.
	// +optional
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`
}

// GetConditions returns the conditions of the ComponentVersion.
func (in *Bootstrap) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the conditions of the ComponentVersion.
func (in *Bootstrap) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// GetRequeueAfter returns the duration after which the ComponentVersion must be
// reconciled again.
func (in *Bootstrap) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Bootstrap is the Schema for the bootstraps API.
type Bootstrap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BootstrapSpec   `json:"spec,omitempty"`
	Status BootstrapStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BootstrapList contains a list of Bootstrap.
type BootstrapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bootstrap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bootstrap{}, &BootstrapList{})
}
