# FreeIPA Issuer

A [cert-manager](https://cert-manager.io) external issuer to be used with [FreeIPA](https://www.freeipa.org/).

## Prerequisite

- kubernetes
- cert-manager **1.0+**

## Install

### kustomize

`kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
metadata:
  name: freeipa-issuer

commonLabels:
  app: freeipa-issuer

resources:
  - https://github.com/guilhem/freeipa-issuer/config/default
```

## Configuration

[examples](config/samples)

### Issuer

An issuer is namespaced

```yaml
apiVersion: certmanager.freeipa.org/v1beta1
kind: Issuer
metadata:
  name: issuer-sample
spec:
  host: freeipa.example.test
  user:
    name: freeipa-auth
    key: user
  password:
    name: freeipa-auth
    key: password

  # Optionals
  serviceName: HTTP
  addHost: true
  addService: true
  addPrincipal: true
  ca: ipa
  insecure: false

---
apiVersion: v1
kind: Secret
metadata:
  name: freeipa-auth
data:
  user: b64value
  password: b64value
```
