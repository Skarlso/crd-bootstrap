package breaking

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/pb33f/libopenapi"
	whatchanged "github.com/pb33f/libopenapi/what-changed"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const schemaEnvelopeTemplate = `openapi: "3.0.0"
info:
  title: crd-schema
  version: "1.0.0"
paths: {}
components:
  schemas:
    Root:
      %s`

func DetectBreakingChanges(oldCRD, newCRD *apiextensionsv1.CustomResourceDefinition) ([]string, error) {
	var breaking []string
	newVersions := make(map[string]*apiextensionsv1.JSONSchemaProps)

	for _, v := range newCRD.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			newVersions[v.Name] = v.Schema.OpenAPIV3Schema
		}
	}

	for _, oldVer := range oldCRD.Spec.Versions {
		if oldVer.Schema == nil || oldVer.Schema.OpenAPIV3Schema == nil {
			continue
		}

		newSchema, ok := newVersions[oldVer.Name]
		if !ok {
			breaking = append(breaking, fmt.Sprintf("version %q removed", oldVer.Name))

			continue
		}

		if reflect.DeepEqual(oldVer.Schema.OpenAPIV3Schema, newSchema) {
			continue
		}

		changes, err := compareSchemas(oldVer.Schema.OpenAPIV3Schema, newSchema)
		if err != nil {
			return nil, fmt.Errorf("comparing version %s: %w", oldVer.Name, err)
		}

		for _, c := range changes {
			breaking = append(breaking, fmt.Sprintf("version %s: %s", oldVer.Name, c))
		}
	}

	return breaking, nil
}

func compareSchemas(oldSchema, newSchema *apiextensionsv1.JSONSchemaProps) ([]string, error) {
	oldDoc, err := schemaToOpenAPIDoc(oldSchema)
	if err != nil {
		return nil, fmt.Errorf("building old schema document: %w", err)
	}

	newDoc, err := schemaToOpenAPIDoc(newSchema)
	if err != nil {
		return nil, fmt.Errorf("building new schema document: %w", err)
	}

	oldModel, err := oldDoc.BuildV3Model()
	if err != nil {
		return nil, fmt.Errorf("building old V3 model: %w", err)
	}

	newModel, err := newDoc.BuildV3Model()
	if err != nil {
		return nil, fmt.Errorf("building new V3 model: %w", err)
	}

	changes := whatchanged.CompareOpenAPIDocuments(oldModel.Model.GoLow(), newModel.Model.GoLow())
	if changes == nil || changes.TotalBreakingChanges() == 0 {
		return nil, nil
	}

	var descriptions []string //nolint:prealloc // no.

	for _, c := range changes.GetAllChanges() {
		if !c.Breaking {
			continue
		}

		desc := c.Property
		if c.Original != "" || c.New != "" {
			desc = fmt.Sprintf("%s: %q -> %q", c.Property, c.Original, c.New)
		}

		descriptions = append(descriptions, desc)
	}

	return descriptions, nil
}

func schemaToOpenAPIDoc(schema *apiextensionsv1.JSONSchemaProps) (libopenapi.Document, error) {
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshaling schema to JSON: %w", err)
	}

	doc := fmt.Sprintf(schemaEnvelopeTemplate, string(schemaJSON))

	return libopenapi.NewDocument([]byte(doc))
}
