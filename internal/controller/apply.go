// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and Open Component Model contributors.
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"bufio"
	"fmt"
	"os"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// readObjects takes a path to a file that contains one or more CRDs and created a list of
// unstructured objects out of them.
func readObjects(manifestPath string) ([]*unstructured.Unstructured, error) {
	fi, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("expected %q to be a file", manifestPath)
	}

	ms, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer ms.Close()

	objects, err := utils.ReadObjects(bufio.NewReader(ms))
	if err != nil {
		return nil, err
	}
	// Make sure we only returns custom resource definitions. We don't want any errand objects be applied to the cluster.
	crds := make([]*unstructured.Unstructured, 0)
	for _, obj := range objects {
		if obj.GetKind() == "CustomResourceDefinition" {
			crds = append(crds, obj)
		}
	}

	return crds, nil
}

// ownerRef contains the server-side apply field manager and ownership labels group.
var ownerRef = ssa.Owner{
	Field: "delivery",
	Group: "crd-bootstrap.delivery.crd-bootstrap",
}

// NewResourceManager creates a ResourceManager for the given cluster.
func NewResourceManager(rcg genericclioptions.RESTClientGetter) (*ssa.ResourceManager, error) {
	cfg, err := rcg.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig failed: %w", err)
	}

	// bump limits
	cfg.QPS = 100.0
	cfg.Burst = 300

	restMapper, err := rcg.ToRESTMapper()
	if err != nil {
		return nil, err
	}

	kubeClient, err := client.New(cfg, client.Options{Mapper: restMapper, Scheme: newScheme()})
	if err != nil {
		return nil, err
	}

	kubePoller := polling.NewStatusPoller(kubeClient, restMapper, polling.Options{})

	return ssa.NewResourceManager(kubeClient, kubePoller, ownerRef), nil
}

func newScheme() *apiruntime.Scheme {
	scheme := apiruntime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	return scheme
}
