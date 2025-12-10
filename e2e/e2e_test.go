package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	clusterName     = "crd-bootstrap-e2e"
	namespace       = "crd-bootstrap-system"
	timeout         = 5 * time.Minute
	pollingInterval = 5 * time.Second
)

func TestE2E(t *testing.T) {
	ctx := context.Background()

	t.Log("Creating kind cluster...")
	if err := createKindCluster(); err != nil {
		t.Fatalf("Failed to create kind cluster: %v", err)
	}
	defer func() {
		t.Log("Cleaning up kind cluster...")
		if err := deleteKindCluster(); err != nil {
			t.Errorf("Failed to delete kind cluster: %v", err)
		}
	}()

	kubeconfig, err := getKindKubeconfig()
	if err != nil {
		t.Fatalf("Failed to get kubeconfig: %v", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		t.Fatalf("Failed to create REST config: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create dynamic client: %v", err)
	}

	t.Log("Building controller image...")
	if err := buildControllerImage(); err != nil {
		t.Fatalf("Failed to build controller image: %v", err)
	}

	t.Log("Loading controller image into kind cluster...")
	if err := loadImageToKind(); err != nil {
		t.Fatalf("Failed to load image to kind: %v", err)
	}

	t.Log("Deploying controller using Helm...")
	if err := deployController(); err != nil {
		t.Fatalf("Failed to deploy controller: %v", err)
	}

	t.Log("Waiting for controller to be ready...")
	if err := waitForControllerReady(ctx, dynamicClient); err != nil {
		t.Fatalf("Controller did not become ready: %v", err)
	}

	t.Log("Applying ConfigMap with CRD...")
	if err := applyManifest("samples/config-map.yaml"); err != nil {
		t.Fatalf("Failed to apply ConfigMap: %v", err)
	}

	t.Log("Applying Bootstrap resource...")
	if err := applyManifest("samples/delivery_v1alpha1_bootstrap_configmap.yaml"); err != nil {
		t.Fatalf("Failed to apply Bootstrap resource: %v", err)
	}

	go func() {
		time.Sleep(5 * time.Second)
		cmd := exec.Command("kubectl", "logs", "-n", namespace, "-l", "app.kubernetes.io/name=crd-bootstrap", "--tail=50", "-f")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}()

	t.Log("Waiting for KrokEvent CRD to be created...")
	if err := waitForCRD(ctx, dynamicClient); err != nil {
		t.Logf("Failed to find CRD, dumping Bootstrap resource...")
		dumpBootstrap()
		t.Fatalf("CRD was not created: %v", err)
	}

	t.Log("Verifying Bootstrap status...")
	if err := verifyBootstrapStatus(ctx, dynamicClient); err != nil {
		t.Logf("Failed Bootstrap status verification, dumping Bootstrap resource...")
		dumpBootstrap()
		t.Fatalf("Bootstrap status verification failed: %v", err)
	}

	t.Log("E2E test passed successfully!")
}

func createKindCluster() error {
	cmd := exec.Command("kind", "create", "cluster", "--name", clusterName, "--wait", "60s")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func deleteKindCluster() error {
	cmd := exec.Command("kind", "delete", "cluster", "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getKindKubeconfig() (string, error) {
	cmd := exec.Command("kind", "get", "kubeconfig", "--name", clusterName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w, output: %s", err, string(output))
	}
	return string(output), nil
}

func buildControllerImage() error {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	cmd := exec.Command("make", "docker-build")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "IMG=crd-bootstrap-controller:e2e")
	return cmd.Run()
}

func loadImageToKind() error {
	cmd := exec.Command("kind", "load", "docker-image", "crd-bootstrap-controller:e2e", "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func deployController() error {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	cmd := exec.Command("kubectl", "create", "namespace", namespace)
	_ = cmd.Run() // Ignore error if namespace already exists

	helmChartPath := filepath.Join(projectRoot, "crd-bootstrap")
	cmd = exec.Command("helm", "install", "crd-bootstrap",
		helmChartPath,
		"--namespace", namespace,
		"--set", "image.repository=crd-bootstrap-controller",
		"--set", "image.tag=e2e",
		"--set", "image.pullPolicy=Never",
		"--wait",
		"--timeout", "5m")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func applyManifest(manifestPath string) error {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	absPath := filepath.Join(projectRoot, manifestPath)

	cmd := exec.Command("kubectl", "apply", "-f", absPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForControllerReady(ctx context.Context, client dynamic.Interface) error {
	gvr := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	return wait.PollUntilContextTimeout(ctx, pollingInterval, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := client.Resource(gvr).Namespace(namespace).Get(ctx, "crd-bootstrap-controller-manager", metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		status, found, err := unstructured.NestedMap(deployment.Object, "status")
		if !found || err != nil {
			return false, nil
		}

		replicas, _, _ := unstructured.NestedInt64(status, "replicas")
		readyReplicas, _, _ := unstructured.NestedInt64(status, "readyReplicas")

		return replicas > 0 && replicas == readyReplicas, nil
	})
}

func waitForCRD(ctx context.Context, client dynamic.Interface) error {
	gvr := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	return wait.PollUntilContextTimeout(ctx, pollingInterval, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.Resource(gvr).Get(ctx, "krokevents.delivery.krok.app", metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

func verifyBootstrapStatus(ctx context.Context, client dynamic.Interface) error {
	gvr := schema.GroupVersionResource{
		Group:    "delivery.crd-bootstrap",
		Version:  "v1alpha1",
		Resource: "bootstraps",
	}

	return wait.PollUntilContextTimeout(ctx, pollingInterval, timeout, true, func(ctx context.Context) (bool, error) {
		bootstrap, err := client.Resource(gvr).Namespace(namespace).Get(ctx, "bootstrap-sample", metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		status, found, err := unstructured.NestedMap(bootstrap.Object, "status")
		if !found || err != nil {
			return false, nil
		}

		// Check if lastAppliedRevision is set
		lastAppliedRevision, found, err := unstructured.NestedString(status, "lastAppliedRevision")
		if !found || err != nil || lastAppliedRevision == "" {
			return false, nil
		}

		// Check for Ready condition
		conditions, found, err := unstructured.NestedSlice(status, "conditions")
		if !found || err != nil {
			return false, nil
		}

		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _, _ := unstructured.NestedString(condMap, "type")
			condStatus, _, _ := unstructured.NestedString(condMap, "status")
			if condType == "Ready" && condStatus == "True" {
				return true, nil
			}
		}

		return false, nil
	})
}

func dumpBootstrap() {
	cmd := exec.Command("kubectl", "get", "bootstrap", "-n", namespace, "bootstrap-sample", "-o", "yaml")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
