# crd-bootstrap

![logo](./hack/crd-bootstrap-logo.png)

Welcome to CRD bootstrapper. The name explains what this controller does. It keeps CRDs in your cluster up-to-date.

Simple, as that. There are three types of bootstrap options.

- URL
- ConfigMap
- GitHub release page

Let's look at each of them.

## URL

There are two ways to fetch CRDs from a URL.

First, by defining a Digest. If a digest is defined together with a URL the operator will _only_ apply the content that
corresponds to the digest.

For example:

```yaml
apiVersion: delivery.crd-bootstrap/v1alpha1
kind: Bootstrap
metadata:
  name: bootstrap-sample
  namespace: crd-bootstrap-system
spec:
  interval: 10s
  source:
    url:
      url: https://raw.githubusercontent.com/krok-o/operator/main/config/crd/bases/delivery.krok.app_krokevents.yaml
  version:
    digest: 7162957068d512154ed353d31b9a0a5a9ff148b4611bd85ba704467a4fcd101a
```

This object will only apply this CRD when the digest matches with the fetched content. The digest is a sum256 digest.
You should be able to produce it by running:

```
sha256sum < krok_event_crd.yaml
```

The second options is to omit this digest. In which case it will keep applying the CRD if there is a new "version"
available. This means, every interval it will download the content and create a digest from it. If that digest does not
match with the last applied digest, it will apply the content.

```yaml
apiVersion: delivery.crd-bootstrap/v1alpha1
kind: Bootstrap
metadata:
  name: bootstrap-sample
  namespace: crd-bootstrap-system
spec:
  interval: 10s
  source:
    url:
      url: https://raw.githubusercontent.com/krok-o/operator/main/config/crd/bases/delivery.krok.app_krokevents.yaml
```

## ConfigMap

To install a set of CRDs from a ConfigMap, simply create a ConfigMap like the one under samples/config.
![configmap](./config/samples/config-map.yaml).

Next, apply a bootstrap CRD:

```yaml
apiVersion: delivery.crd-bootstrap/v1alpha1
kind: Bootstrap
metadata:
  name: bootstrap-sample
  namespace: crd-bootstrap-system
spec:
  interval: 10s
  source:
    configMap:
      name: crd-bootstrap-sample
      namespace: crd-bootstrap-system
  version:
    semver: 1.0.0
```

And done. What this does, we'll get to under [But what does it do?](#but-what-does-it-do).

## GitHub

GitHub is largely the same, but 

## But what does it do?

### Constant Version Reconciliation

The semver that we defined is a constraint. A semver constraint. It could be something like `>=v1`. And anything that
satisfies this constraint gets installed. It only rolls forward, to prevent accidental or intentional upstream version
rollbacks if a later version is removed.

Given the `interval` it checks every time if there is a newer version satisfying the constraint. The CRD keeps track of
the last applied version in its status. Once there is a new one, it applies it to the cluster and saves that version.

It also saves attempted versions. If a version is failed to apply, it will still record it as attempted version in its
status.

## Validation

Before applying a new CRD there are options to make sure that it doesn't break anything by defining a template to check
against. It would be awesome if it could list all Objects that belong to a CRD but that's just not possible because of various
security reasons.

To work around that, the user can define a `template` section in the Bootstrap object. It will use that template and
validate the CRD it's trying to apply to the cluster first against that template:

```yaml
apiVersion: delivery.crd-bootstrap/v1alpha1
kind: Bootstrap
metadata:
  name: bootstrap-sample
  namespace: crd-bootstrap-system
spec:
  interval: 10s
  template:
    KrokEvent:
      apiVersion: delivery.krok.app/v1alpha1
      kind: KrokEvent
      metadata:
        name: krokevent-sample
      spec:
        thisfield: bla
  source:
    configMap:
      name: crd-bootstrap-sample
      namespace: crd-bootstrap-system
  version:
    semver: 1.0.0
```

The template is a map of `Kind`: `Template Yaml`. Here, we have a KrokEvent CRD kind. This fails validation because the
spec field doesn't have `thisfield` in it. A failed validation will immediately stop reconciliation of the bootstrap
object. User intervention is required to kick it off again to prevent messing up the cluster.

If it's desired to continue on failures, there is a setting for that. Simply set `continueOnValidationError: true` in the
Bootstrap's spec.

## Multiple CRDs in a single file

A single Bootstrap CRD will point to a single file of ConfigMap. But that file, or ConfigMap may contain multiple CRDs.
Once a Bootstrap object is deleted it will remove all CRDs that belong to it and were applied by it.

For example, consider the GitHub example. Flux's `install.yaml` contains all their objects. And it contains Deployment
and Service objects too. Bootstrap doesn't care. It only installs the CRDs from that by using server-side-apply.

The status of the Bootstrap object will keep track of what CRDs it installed.
## Contributing

Contributions are always welcomed.

## How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
which provide a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

## Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

## Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## Using Tilt

This project uses [tilt](https://tilt.dev/). For local development, create a kind cluster with:

```
kind create cluster
```

... and then simply execute `tilt up`. Hit space, and you should see everything preloaded.

## License

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

