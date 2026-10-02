# FreeIPA Issuer

[![CI](https://github.com/guilhem/freeipa-issuer/actions/workflows/ci.yml/badge.svg)](https://github.com/guilhem/freeipa-issuer/actions/workflows/ci.yml)
[![CodeQL](https://github.com/guilhem/freeipa-issuer/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/guilhem/freeipa-issuer/actions/workflows/codeql-analysis.yml)

A [cert-manager](https://cert-manager.io) external issuer to be used with [FreeIPA](https://www.freeipa.org/).

## Prerequisite

- Kubernetes
- cert-manager with the [CertificateRequest approval](https://cert-manager.io/docs/concepts/certificaterequest/#approval) API (1.3 or later). The controller is built against the cert-manager 1.21 API.
- `kubectl` (or [kustomize](https://github.com/kubernetes-sigs/kustomize)) to install the manifests
- A container image of the controller. The manifests reference `controller:latest` as a placeholder: build and push your own from the [Dockerfile](Dockerfile) (see below).
- optional: Kubernetes worker nodes adopted into FreeIPA domain (for use with self signed certificate)

## Install

### Build the image

```sh
# multi-arch (amd64 and arm64) image
docker buildx build --platform linux/amd64,linux/arm64 -t registry.example.com/freeipa-issuer:latest --push .
```

### kustomize

`kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/guilhem/freeipa-issuer//config/default?ref=master

images:
  - name: controller
    newName: registry.example.com/freeipa-issuer
    newTag: latest
```

`kubectl apply -k .` installs the CRDs and the controller in the `freeipa-issuer-system` namespace.

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

  # Optionals (defaults shown)
  serviceName: HTTP
  addHost: true
  addService: true
  addPrincipal: true
  ca: ipa
  # Ignore errors when looking for or adding the service
  ignoreError: false

---
apiVersion: v1
kind: Secret
metadata:
  name: freeipa-auth
stringData:
  user: freeipa-user
  password: freeipa-password
```

The FreeIPA user needs the rights to request certificates and, depending on
`addHost` and `addService`, to add hosts and services.

A `ClusterIssuer` takes the same `spec`; its secrets must set a `namespace` (see
[the sample](config/samples/certmanager_v1beta1_clusterissuer.yaml)).

### Trust the FreeIPA certificate

The controller checks the certificate of the FreeIPA server against the CA
certificates of its container, which only contain public CAs. Give it the CA of
your FreeIPA server (`/etc/ipa/ca.crt` on an IPA server or client) and point the
standard Go variable `SSL_CERT_FILE` at it. Add this to your `kustomization.yaml`
next to the `images` above:

```yaml
configMapGenerator:
  - name: freeipa-ca
    namespace: freeipa-issuer-system
    files:
      - ca.crt

patches:
  - patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: freeipa-issuer-controller-manager
        namespace: freeipa-issuer-system
      spec:
        template:
          spec:
            containers:
              - name: manager
                env:
                  - name: SSL_CERT_FILE
                    value: /etc/freeipa-ca/ca.crt
                volumeMounts:
                  - name: freeipa-ca
                    mountPath: /etc/freeipa-ca
                    readOnly: true
            volumes:
              - name: freeipa-ca
                configMap:
                  name: freeipa-ca
```

`insecure: true` in the issuer spec skips the verification of the FreeIPA
certificate altogether. Use it for tests only.

### Disable Approval Check

The FreeIPA Issuer will wait for CertificateRequests to have an [approved
condition
set](https://cert-manager.io/docs/concepts/certificaterequest/#approval) before
signing. The default install lets cert-manager's internal approver approve every
CertificateRequest for these issuers
([RBAC](config/rbac/cert_manager_controller_approver_clusterrole.yaml)). If you
drop it, approve the requests yourself, for example with
[approver-policy](https://cert-manager.io/docs/policy/approval/approver-policy/).

You can make the controller sign without waiting for an approval by supplying
the command line flag `-disable-approved-check` to the controller Deployment.
Every CertificateRequest that references one of the issuers is then signed, so
only do this if nothing else in your cluster relies on approval. A denied
request is never signed.

### Metrics

The default install serves metrics over HTTPS on port 8443 (`-metrics-addr`) with a
cert-manager Certificate for
`freeipa-issuer-controller-manager-metrics-service.freeipa-issuer-system.svc`.
The built-in `cert-manager.io` SelfSigned Issuer bootstraps it independently of
FreeIPA. The controller waits for the required `metrics-server-cert` Secret;
cert-manager must be running and allowed to approve its own CertificateRequests.

The Secret is mounted read-only at `/etc/metrics-certs` (`--metrics-cert-dir`).
Missing or invalid certificate files cause startup to fail; certificate updates
are reloaded by controller-runtime. Custom mounts must use the whole directory,
without `subPath`, so Kubernetes can project renewed certificates.

Scrapers need both a trusted certificate and a token allowed to `get /metrics`:
bind the `freeipa-issuer-metrics-reader` ClusterRole to the scraper's ServiceAccount.
Retrieve the public trust material through your authenticated Kubernetes access:

```sh
kubectl -n freeipa-issuer-system wait --for=condition=Ready --timeout=180s certificate/freeipa-issuer-metrics-serving-cert
kubectl -n freeipa-issuer-system get secret metrics-server-cert -o jsonpath='{.data.tls\.crt}' | base64 --decode > metrics-ca.crt
```

Configure the scraper to trust this certificate and verify the Service DNS name.
The self-signed certificate and key rotate on renewal: refresh the scraper's
trust bundle whenever the Certificate changes. For an existing production trust
setup, change the Certificate's `issuerRef` to an independent CA-backed issuer
and distribute that CA's trust bundle instead. Do not use this FreeIPA controller
to issue its own startup certificate. See [cert-manager trust guidance](https://cert-manager.io/docs/configuration/selfsigned/#trust).

If an overlay changes the namespace or name prefix, also patch the Certificate's
`spec.dnsNames` to the final Service DNS name. Its `secretName` and the Deployment
volume reference must remain identical.

HTTPS and authentication are always enabled. `-metrics-addr=0` disables the
listener; to remove the certificate startup dependency too, remove the metrics
volume and volume mount from the Deployment. Running locally without
`--metrics-cert-dir` uses an ephemeral localhost certificate, unsuitable for
scraping through a Kubernetes Service.

## Usage

CertificateRequests must explicitly set `spec.issuerRef.group` to
`certmanager.freeipa.org`. An omitted group belongs to cert-manager's built-in
issuers and is ignored by this controller, even if a FreeIPA Issuer has the same
name. When upgrading, add this group to any handwritten FreeIPA requests that
previously omitted it.

### Secure an Ingress resource

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example-ingress
  annotations:
    #Specify the name of the issuer to use must be in the same namespace
    cert-manager.io/issuer: issuer-sample
    #The group of the out of tree issuer is needed for cert-manager to find it
    cert-manager.io/issuer-group: certmanager.freeipa.org
    #Specify a common name for the certificate (the FreeIPA Issuer requires one)
    cert-manager.io/common-name: www.example.com

spec:
  ingressClassName: traefik
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
            pathType: Prefix
            backend:
              service:
                name: backend
                port:
                  number: 80
```

## Development

`make test` regenerates the code and the manifests, then runs the tests. It
downloads a kube-apiserver and etcd for the tests that need one
([envtest](https://book.kubebuilder.io/reference/envtest)). The tests do not
need a FreeIPA server and do not sign anything.
