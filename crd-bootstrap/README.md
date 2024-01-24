# Official Helm Charts for crd-bootstrap controller

## Installation

We are using ghcr.io's OCI registry for publishing helm charts.

To install it, simply run:

```
helm upgrade -i --wait --create-namespace -n crd-bootstrap crd-bootstrap \
  oci://ghcr.io/skarlso/helm/crd-bootstrap --version <VERSION>
```

## Configuration

The project is using plain Helm Values files for configuration options.
Check out the default values for the chart [here](https://artifacthub.io/packages/helm/crd-bootstrap/crd-bootstrap?modal=values).
