package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	certutil "k8s.io/client-go/util/cert"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestManagerMetrics runs the real binary with a ServiceAccount token and only
// the rendered deployment RBAC. Admin credentials are used solely for fixtures.
// envtest has no pod networking or in-cluster namespace file, so the subprocess
// uses loopback and leaves leader election off; the rendered wiring is checked
// separately below. No cert-manager controller or FreeIPA server is involved.
func TestManagerMetrics(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" && os.Getenv("CI") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run `make test` to get the envtest binaries")
	}
	ctx := t.Context()
	run := func(name string, args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return out
	}
	bin := filepath.Join(t.TempDir(), "manager")
	run("go", "build", "-o", bin, ".")
	moduleDir := strings.TrimSpace(string(run("go", "list", "-m", "-f", "{{.Dir}}", "github.com/cert-manager/cert-manager")))
	local := false
	env := &envtest.Environment{
		UseExistingCluster:    &local,
		CRDDirectoryPaths:     []string{"config/crd/bases", filepath.Join(moduleDir, "deploy", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	create := func(obj client.Object) {
		t.Helper()
		if err := admin.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}

	var deployment appsv1.Deployment
	var service corev1.Service
	rendered := run("make", "--no-print-directory", "-s", "build-manifests")
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch obj.GetKind() {
		case "Namespace", "Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding":
			create(obj)
		case "Deployment":
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &deployment); err != nil {
				t.Fatal(err)
			}
		case "Service":
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &service); err != nil {
				t.Fatal(err)
			}
		}
	}
	ns := deployment.Namespace
	sa := deployment.Spec.Template.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	if ns != "freeipa-issuer-system" || len(deployment.Spec.Template.Spec.Containers) != 1 || len(service.Spec.Ports) != 1 {
		t.Fatalf("unexpected rendered deployment or service: %+v / %+v", deployment.Spec, service.Spec)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	port := service.Spec.Ports[0]
	if service.Namespace != ns || !maps.Equal(service.Spec.Selector, deployment.Spec.Template.Labels) ||
		port.Port != 8443 || len(container.Ports) != 1 || port.TargetPort.String() != container.Ports[0].Name ||
		container.Ports[0].ContainerPort != 8443 || !strings.Contains(strings.Join(container.Args, " "), "--metrics-addr=:8443") {
		t.Fatalf("metrics Service does not target the deployed listener: %+v / %+v", service.Spec, container)
	}
	managerSubject := rbacv1.Subject{Kind: "ServiceAccount", Name: sa, Namespace: ns}
	for _, name := range []string{"freeipa-issuer-manager-rolebinding", "freeipa-issuer-proxy-rolebinding", "freeipa-issuer-leader-election-rolebinding"} {
		var subjects []rbacv1.Subject
		if strings.Contains(name, "leader-election") {
			binding := &rbacv1.RoleBinding{}
			if err := admin.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, binding); err != nil {
				t.Fatal(err)
			}
			subjects = binding.Subjects
		} else {
			binding := &rbacv1.ClusterRoleBinding{}
			if err := admin.Get(ctx, client.ObjectKey{Name: name}, binding); err != nil {
				t.Fatal(err)
			}
			subjects = binding.Subjects
		}
		if len(subjects) != 1 || subjects[0] != managerSubject {
			t.Fatalf("%s subjects %v do not match deployed ServiceAccount %v", name, subjects, managerSubject)
		}
	}
	t.Logf("rendered wiring: namespace=%s ServiceAccount=%s Service 8443 -> https -> container 8443", ns, sa)

	token := func(name string) string {
		t.Helper()
		create(&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}})
		tok, err := kube.CoreV1().ServiceAccounts(ns).CreateToken(ctx, name, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return tok.Status.Token
	}
	managerToken, readerToken, deniedToken := token(sa), token("metrics-reader"), token("no-metrics-access")
	create(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-metrics-reader"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "freeipa-issuer-metrics-reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "metrics-reader", Namespace: ns}},
	})
	managerCfg := rest.AnonymousClientConfig(cfg)
	managerCfg.BearerToken = managerToken
	managerKube, err := kubernetes.NewForConfig(managerCfg)
	if err != nil {
		t.Fatal(err)
	}
	self, err := managerKube.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil || self.Status.UserInfo.Username != "system:serviceaccount:"+ns+":"+sa {
		t.Fatalf("unexpected manager identity: %v (%v)", self, err)
	}
	access, err := managerKube.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
			Group: rbacv1.GroupName, Resource: "clusterroles", Verb: "create",
		}},
	}, metav1.CreateOptions{})
	if err != nil || access.Status.Allowed {
		t.Fatalf("manager must not have admin privileges: %v (%v)", access, err)
	}
	t.Logf("manager identity=%s; create clusterroles denied", self.Status.UserInfo.Username)
	kubeconfig := clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"envtest": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"manager": {Token: managerToken}},
		Contexts:       map[string]*clientcmdapi.Context{"manager": {Cluster: "envtest", AuthInfo: "manager", Namespace: ns}},
		CurrentContext: "manager",
	}
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"shipped-rbac", "missing-proxy-binding", "missing-subjectaccessreviews"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "missing-proxy-binding" {
				binding := &rbacv1.ClusterRoleBinding{}
				if err := admin.Get(ctx, client.ObjectKey{Name: "freeipa-issuer-proxy-rolebinding"}, binding); err != nil {
					t.Fatal(err)
				}
				if err := admin.Delete(ctx, binding); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { binding.ResourceVersion = ""; create(binding) })
			}
			if mode == "missing-subjectaccessreviews" {
				role := &rbacv1.ClusterRole{}
				if err := admin.Get(ctx, client.ObjectKey{Name: "freeipa-issuer-proxy-role"}, role); err != nil {
					t.Fatal(err)
				}
				// This is a subset of shipped permissions: TokenReviews only.
				role.Rules = role.Rules[:1]
				if len(role.Rules[0].Resources) != 1 || role.Rules[0].Resources[0] != "tokenreviews" {
					t.Fatal("unexpected proxy-role rules")
				}
				if err := admin.Update(ctx, role); err != nil {
					t.Fatal(err)
				}
			}
			addr, httpsClient := startTestManager(t, bin, kubeconfigPath)
			check := func(name, token string, want int, bodyContains string) {
				t.Helper()
				var last string
				err := wait.PollUntilContextTimeout(t.Context(), 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/metrics", nil)
					if err != nil {
						return false, err
					}
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					res, err := httpsClient.Do(req)
					if err != nil {
						last = err.Error()
						return false, nil
					}
					defer res.Body.Close()
					body, err := io.ReadAll(res.Body)
					last = fmt.Sprintf("status=%d body=%s", res.StatusCode, body)
					return err == nil && res.StatusCode == want && strings.Contains(string(body), bodyContains), nil
				})
				if err != nil {
					t.Fatalf("%s: want %d: %v; %s", name, want, err, last)
				}
				t.Logf("%s: HTTP %d", name, want)
			}
			check("no token", "", http.StatusUnauthorized, "Unauthorized")
			switch mode {
			case "shipped-rbac":
				check("token without metrics-reader", deniedToken, http.StatusForbidden, "Authorization denied")
				check("metrics-reader", readerToken, http.StatusOK, "controller_runtime_reconcile_total")
				// A missing Secret keeps this fixture offline while proving that the
				// non-admin controller can read and update an Issuer's status.
				issuer := &api.Issuer{ObjectMeta: metav1.ObjectMeta{Name: "offline", Namespace: ns}, Spec: api.IssuerSpec{
					Host:     "unused.example.test",
					User:     &api.SecretKeySelector{SecretReference: corev1.SecretReference{Name: "missing"}, Key: "user"},
					Password: &api.SecretKeySelector{SecretReference: corev1.SecretReference{Name: "missing"}, Key: "password"},
				}}
				create(issuer)
				if err := wait.PollUntilContextTimeout(t.Context(), 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
					if err := admin.Get(ctx, client.ObjectKeyFromObject(issuer), issuer); err != nil {
						return false, err
					}
					return len(issuer.Status.Conditions) > 0 && issuer.Status.Conditions[0].Reason == "NotFound", nil
				}); err != nil {
					t.Fatalf("non-admin reconciliation: %v", err)
				}
				t.Log("non-admin Issuer reconciliation: Ready=False/NotFound for missing Secret")
			case "missing-proxy-binding":
				check("TokenReview forbidden: fail closed", readerToken, http.StatusInternalServerError, "Authentication failed")
			case "missing-subjectaccessreviews":
				check("SubjectAccessReview forbidden: fail closed", readerToken, http.StatusInternalServerError, "Authorization for user")
			}
		})
	}
	t.Run("insecure-flag-rejected", func(t *testing.T) {
		out, err := exec.CommandContext(t.Context(), bin, "--metrics-secure=false").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "flag provided but not defined") {
			t.Fatalf("insecure flag accepted: %v\n%s", err, out)
		}
	})
}

func startTestManager(t *testing.T, bin, kubeconfig string) (string, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	// Give only this local subprocess a trusted serving certificate. This tests
	// HTTPS without a TLS verification bypass, using controller-runtime's CertDir.
	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey("localhost", []net.IP{net.ParseIP("127.0.0.1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	certDir := filepath.Join(dir, "k8s-metrics-server", "serving-certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM} {
		if err := os.WriteFile(filepath.Join(certDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "manager.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--metrics-addr="+addr)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig, "TMPDIR="+dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		stopped := make(chan error, 1)
		go func() { stopped <- cmd.Wait() }()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("manager exited: %v", err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-stopped
			t.Error("manager did not stop gracefully")
		}
		_ = log.Close()
		out, _ := os.ReadFile(logPath)
		if t.Failed() {
			t.Logf("manager log:\n%s", out)
		}
	})
	return addr, &http.Client{Transport: transport, Timeout: 12 * time.Second}
}
