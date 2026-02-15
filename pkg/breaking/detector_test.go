package breaking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestDetectBreakingChanges_NoBreaking_AddOptionalField(t *testing.T) {
	old := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name": {Type: "string"},
	})
	new := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name":  {Type: "string"},
		"email": {Type: "string"},
	})

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestDetectBreakingChanges_Breaking_ChangeType(t *testing.T) {
	old := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"count": {Type: "integer"},
	})
	new := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"count": {Type: "string"},
	})

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.NotEmpty(t, changes)
}

func TestDetectBreakingChanges_Breaking_RemoveField(t *testing.T) {
	old := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name": {Type: "string"},
		"age":  {Type: "integer"},
	})
	new := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name": {Type: "string"},
	})

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.NotEmpty(t, changes)
}

func TestDetectBreakingChanges_VersionRemoved(t *testing.T) {
	old := &apiextensionsv1.CustomResourceDefinition{}
	old.Name = "test.example.com"
	old.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
		versionWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
			"name": {Type: "string"},
		}),
		versionWithSchema("v2", map[string]apiextensionsv1.JSONSchemaProps{
			"name": {Type: "string"},
		}),
	}

	new := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name": {Type: "string"},
	})

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.Contains(t, changes, `version "v2" removed`)
}

func TestDetectBreakingChanges_NewCRD_NoOld(t *testing.T) {
	old := &apiextensionsv1.CustomResourceDefinition{}
	new := crdWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
		"name": {Type: "string"},
	})

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestDetectBreakingChanges_MultipleVersions(t *testing.T) {
	old := &apiextensionsv1.CustomResourceDefinition{}
	old.Name = "test.example.com"
	old.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
		versionWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
			"name": {Type: "string"},
		}),
		versionWithSchema("v2", map[string]apiextensionsv1.JSONSchemaProps{
			"count": {Type: "integer"},
		}),
	}

	new := &apiextensionsv1.CustomResourceDefinition{}
	new.Name = "test.example.com"
	new.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
		versionWithSchema("v1", map[string]apiextensionsv1.JSONSchemaProps{
			"name": {Type: "string"},
		}),
		versionWithSchema("v2", map[string]apiextensionsv1.JSONSchemaProps{
			"count": {Type: "string"},
		}),
	}

	changes, err := DetectBreakingChanges(old, new)
	require.NoError(t, err)
	assert.NotEmpty(t, changes)

	hasV2Change := false
	for _, c := range changes {
		if len(c) > 10 && c[:10] == "version v2" {
			hasV2Change = true
		}
	}
	assert.True(t, hasV2Change)
}

func crdWithSchema(version string, properties map[string]apiextensionsv1.JSONSchemaProps) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	crd.Name = "test.example.com"
	crd.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
		versionWithSchema(version, properties),
	}
	return crd
}

func versionWithSchema(name string, properties map[string]apiextensionsv1.JSONSchemaProps) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{
		Name: name,
		Schema: &apiextensionsv1.CustomResourceValidation{
			OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
				Type:       "object",
				Properties: properties,
			},
		},
	}
}
