# FreeIPA Issuer

[![CodeQL](https://github.com/guilhem/freeipa-issuer/workflows/CodeQL/badge.svg)](https://github.com/guilhem/freeipa-issuer/actions?query=workflow%3ACodeQL)

A [cert-manager](https://cert-manager.io) external issuer to be used with [FreeIPA](https://www.freeipa.org/).

## Prerequisite

- kubernetes
- cert-manager **1.0+**
- [kustomize](https://github.com/kubernetes-sigs/kustomize)
- optional: Kubernetes worker nodes adopted into FreeIPA domain (for use with self signed certificate)

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
  # Do not check certificate of IPA server connection
  insecure: true # unless you can create your own container and inject IPA server CA as trusted.
  # This fixes a bug when adding a service
  ignoreError: true

---
apiVersion: v1
kind: Secret
metadata:
  name: freeipa-auth
data:
  user: b64value
  password: b64value
```

### Disable Approval Check

The FreeIPA Issuer will wait for CertificateRequests to have an [approved
condition
set](https://cert-manager.io/docs/concepts/certificaterequest/#approval) before
signing. If using an older version of cert-manager (pre v1.3), you can disable
this check by supplying the command line flag `-disable-approved-check` to the
Issuer Deployment.

## Usage

### Secure an Ingress resource

```yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: example-ingress
  annotations:
    kubernetes.io/ingress.class: traefik
    #Specify the name of the issuer to use must be in the same namespace
    cert-manager.io/issuer: freeipa-issuer
    #The group of the out of tree issuer is needed for cert-manager to find it
    cert-manager.io/issuer-group: certmanager.freeipa.org
    #Specify a common name for the certificate
    cert-manager.io/common-name: www.example.com

spec:
  #placing a host in the TLS config will indicate a certificate should be created
  tls:
    - hosts:
      - www.example.com
      #The certificate will be stored in this secret
      secretName: example-cert
  rules:
    - host: www.example.com
      http:
        paths:
          - path: /
            backend:
              serviceName: backend
              servicePort: 80
```
