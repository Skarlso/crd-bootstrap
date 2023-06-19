package configmap

import "sigs.k8s.io/controller-runtime/pkg/client"

// Source defines a source that can fetch CRD data from a config map.
type Source struct {
	client client.Client
}

func NewSource(client client.Client) *Source {
	return &Source{client: client}
}
