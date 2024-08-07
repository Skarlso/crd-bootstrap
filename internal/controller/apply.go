package controller

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
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

// NewResourceManager creates a ResourceManager for the given cluster.
func (r *BootstrapReconciler) NewResourceManager(ctx context.Context, obj *v1alpha1.Bootstrap) (*ssa.ResourceManager, error) {
	ownerRef := ssa.Owner{
		Field: "delivery",
		Group: "crd-bootstrap.delivery.crd-bootstrap",
	}

	statusPoller := polling.NewStatusPoller(r.Client, r.Client.RESTMapper(), polling.Options{})

	// Configure the Kubernetes client for impersonation.
	impersonation := runtimeClient.NewImpersonator(
		r.Client,
		statusPoller,
		polling.Options{},
		obj.Spec.KubeConfig.SecretRef,
		runtimeClient.KubeConfigOptions{},
		r.DefaultServiceAccount,
		obj.Spec.KubeConfig.ServiceAccount,
		obj.Namespace,
	)

	// Create the Kubernetes client that runs under impersonation.
	kubeClient, poller, err := impersonation.GetClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build kube client: %w", err)
	}

	return ssa.NewResourceManager(kubeClient, poller, ownerRef), nil
}
