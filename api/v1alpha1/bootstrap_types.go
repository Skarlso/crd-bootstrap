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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GitHub defines a GitHub type source where the CRD is coming from `release` section of a GitHub repository.
type GitHub struct {
}

// ConfigMap defines a reference to a configmap which hold the CRD information. Version is taken from a version field.
type ConfigMap struct {
	// +required
	Name string `json:"name"`
	// +required
	Version string `json:"version"`
}

// URL holds a URL from which to fetch the CRD. Version is defined through the digest of the content.
type URL struct {
	URL string `json:"url"`
}

// Source defines options from where to fetch CRD content.
type Source struct {
	// +optional
	GitHub *GitHub `json:"gitHub,omitempty"`
	// +optional
	ConfigMap *ConfigMap `json:"configMap,omitempty"`
	// +optional
	URL *URL `json:"url,omitempty"`
}

// BootstrapSpec defines the desired state of Bootstrap
type BootstrapSpec struct {
	// Interval defines the regular interval at which a poll for new version should happen.
	// +optional
	Interval metav1.Time `json:"interval,omitempty"`

	// SourceRef defines a reference to a source which will provide a CRD based on some contract.
	// +required
	Source *Source `json:"source"`

	// TemplateRef defines a reference to a configmap which holds a template that we will use to verify that
	// the CRD doesn't break anything if applied.
	// +required
	TemplateRef string `json:"templateRef"`
}

// BootstrapStatus defines the observed state of Bootstrap
type BootstrapStatus struct {
	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
	// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	LastAttemptedVersion string `json:"lastAttemptedVersion,omitempty"`

	// +optional
	LastAppliedVersion string `json:"lastAppliedVersion,omitempty"`

	// +optional
	LastAppliedDigest string `json:"lastAppliedDigest,omitempty"`
}

// GetConditions returns the conditions of the ComponentVersion.
func (in *Bootstrap) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the conditions of the ComponentVersion.
func (in *Bootstrap) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Bootstrap is the Schema for the bootstraps API
type Bootstrap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BootstrapSpec   `json:"spec,omitempty"`
	Status BootstrapStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BootstrapList contains a list of Bootstrap
type BootstrapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bootstrap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bootstrap{}, &BootstrapList{})
}
