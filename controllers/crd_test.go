package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
)

// TestGeneratedCRDs installs the generated CRDs in a real kube-apiserver and
// checks that host, user and ignoreError survive for both kinds. A CRD that
// drifts from the Go type makes the API server silently prune fields.
func TestGeneratedCRDs(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" && os.Getenv("CI") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run `make test` to get the envtest binaries")
	}

	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	spec := api.IssuerSpec{
		Host:         "ipa.example.test",
		User:         &api.SecretKeySelector{Key: "user"},
		Password:     &api.SecretKeySelector{Key: "password"},
		ServiceName:  "HTTP",
		AddHost:      true,
		AddService:   true,
		AddPrincipal: true,
		Ca:           "ipa",
		Insecure:     false,
		IgnoreError:  true,
	}
	meta := metav1.ObjectMeta{Name: "ipa", Namespace: "default"}
	ctx := context.Background()

	if err := c.Create(ctx, &api.Issuer{ObjectMeta: meta, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	gotIssuer := &api.Issuer{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ipa"}, gotIssuer); err != nil {
		t.Fatal(err)
	}
	if *gotIssuer.Spec.User != *spec.User || gotIssuer.Spec.Host != spec.Host || !gotIssuer.Spec.IgnoreError {
		t.Fatalf("Issuer spec changed in the API server: %+v", gotIssuer.Spec)
	}

	if err := c.Create(ctx, &api.ClusterIssuer{ObjectMeta: metav1.ObjectMeta{Name: "ipa"}, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	gotCluster := &api.ClusterIssuer{}
	if err := c.Get(ctx, types.NamespacedName{Name: "ipa"}, gotCluster); err != nil {
		t.Fatal(err)
	}
	if *gotCluster.Spec.User != *spec.User || gotCluster.Spec.Host != spec.Host || !gotCluster.Spec.IgnoreError {
		t.Fatalf("ClusterIssuer spec changed in the API server (ignoreError pruned?): %+v", gotCluster.Spec)
	}
}
