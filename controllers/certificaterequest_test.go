package controllers

import (
	"context"
	"reflect"
	"testing"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	provisioners "github.com/guilhem/freeipa-issuer/provisionners"
)

func condition(t cmapi.CertificateRequestConditionType, s cmmeta.ConditionStatus, reason string) cmapi.CertificateRequestCondition {
	return cmapi.CertificateRequestCondition{Type: t, Status: s, Reason: reason}
}

// An omitted group belongs to cert-manager.io, even when its issuer name
// collides with a Ready FreeIPA Issuer and the request has been approved.
func TestApprovedEmptyGroupIsIgnored(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cmapi.AddToScheme, api.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	key := types.NamespacedName{Namespace: "empty-group-boundary", Name: "ipa"}
	cr := &cmapi.CertificateRequest{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: "cr"},
		Spec: cmapi.CertificateRequestSpec{
			IssuerRef: cmmeta.IssuerReference{Kind: "Issuer", Name: key.Name},
			// A sentinel provisioner below fails CSR decoding if signing is attempted.
			Request: []byte("must never be sent for signing"),
		},
		Status: cmapi.CertificateRequestStatus{Conditions: []cmapi.CertificateRequestCondition{
			condition(cmapi.CertificateRequestConditionApproved, cmmeta.ConditionTrue, "test"),
		}},
	}
	issuer := &api.Issuer{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Status: api.IssuerStatus{Conditions: []api.IssuerCondition{
			{Type: api.ConditionReady, Status: api.ConditionTrue},
		}},
	}
	provisioners.Store(key, &provisioners.FreeIPAPKI{})
	t.Cleanup(func() { provisioners.Store(key, nil) })
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, issuer).
		WithStatusSubresource(cr, issuer).Build()
	before := &cmapi.CertificateRequest{}
	requestKey := client.ObjectKeyFromObject(cr)
	if err := base.Get(context.Background(), requestKey, before); err != nil {
		t.Fatal(err)
	}
	issuerLookups, statusWrites := 0, 0
	c := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*api.Issuer); ok {
				issuerLookups++
			}
			return c.Get(ctx, key, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			statusWrites++
			return c.SubResource(sub).Update(ctx, obj, opts...)
		},
	})
	r := &CertificateRequestReconciler{Client: c, Scheme: scheme, CheckApprovedCondition: true}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: requestKey}); err != nil {
		t.Errorf("foreign request reached processing or signing: %v", err)
	}
	if issuerLookups != 0 || statusWrites != 0 {
		t.Errorf("foreign request caused %d issuer lookups and %d status writes", issuerLookups, statusWrites)
	}
	after := &cmapi.CertificateRequest{}
	if err := base.Get(context.Background(), requestKey, after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("foreign request changed: before=%+v after=%+v", before, after)
	}
}

// TestCertificateRequestReconcileBoundaries checks which CertificateRequests the
// controller refuses to sign. None of these cases may reach FreeIPA: the
// reconciler has to stop (or fail) before a provisioner is used.
func TestCertificateRequestReconcileBoundaries(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, cmapi.AddToScheme, api.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	ours := cmmeta.IssuerReference{Group: api.GroupVersion.Group, Kind: "Issuer", Name: "ipa"}
	approved := condition(cmapi.CertificateRequestConditionApproved, cmmeta.ConditionTrue, "test")
	denied := condition(cmapi.CertificateRequestConditionDenied, cmmeta.ConditionTrue, "test")

	tests := []struct {
		name         string
		checkApprove bool
		ref          cmmeta.IssuerReference
		isCA         bool
		conditions   []cmapi.CertificateRequestCondition
		issuer       *api.Issuer
		wantErr      bool
		// wantReady is the expected Ready condition reason ("" means no Ready condition).
		wantReady  string
		wantStatus cmmeta.ConditionStatus
	}{
		{
			name:         "request for another issuer group is ignored",
			checkApprove: true,
			ref:          cmmeta.IssuerReference{Group: "cert-manager.io", Kind: "Issuer", Name: "ipa"},
			conditions:   []cmapi.CertificateRequestCondition{approved},
		},
		{
			name:         "unapproved request is not processed",
			checkApprove: true,
			ref:          ours,
		},
		{
			name:         "denied request is marked Denied",
			checkApprove: true,
			ref:          ours,
			conditions:   []cmapi.CertificateRequestCondition{denied},
			wantReady:    cmapi.CertificateRequestReasonDenied,
			wantStatus:   cmmeta.ConditionFalse,
		},
		{
			name:         "denied request is marked Denied even when the approval check is disabled",
			checkApprove: false,
			ref:          ours,
			conditions:   []cmapi.CertificateRequestCondition{denied},
			wantReady:    cmapi.CertificateRequestReasonDenied,
			wantStatus:   cmmeta.ConditionFalse,
		},
		{
			name:         "CA request is not signed",
			checkApprove: true,
			ref:          ours,
			isCA:         true,
			conditions:   []cmapi.CertificateRequestCondition{approved},
		},
		{
			name:         "approved request with a missing issuer stays Pending",
			checkApprove: true,
			ref:          ours,
			conditions:   []cmapi.CertificateRequestCondition{approved},
			wantErr:      true,
			wantReady:    cmapi.CertificateRequestReasonPending,
			wantStatus:   cmmeta.ConditionFalse,
		},
		{
			name:         "approved request with a not Ready issuer stays Pending",
			checkApprove: true,
			ref:          ours,
			conditions:   []cmapi.CertificateRequestCondition{approved},
			issuer:       &api.Issuer{ObjectMeta: metav1.ObjectMeta{Name: "ipa", Namespace: "ns"}},
			wantErr:      true,
			wantReady:    cmapi.CertificateRequestReasonPending,
			wantStatus:   cmmeta.ConditionFalse,
		},
		{
			name:         "approved request with a Ready issuer but no provisioner stays Pending",
			checkApprove: true,
			ref:          ours,
			conditions:   []cmapi.CertificateRequestCondition{approved},
			issuer: &api.Issuer{
				ObjectMeta: metav1.ObjectMeta{Name: "ipa", Namespace: "ns"},
				Status: api.IssuerStatus{Conditions: []api.IssuerCondition{
					{Type: api.ConditionReady, Status: api.ConditionTrue},
				}},
			},
			wantErr:    true,
			wantReady:  cmapi.CertificateRequestReasonPending,
			wantStatus: cmmeta.ConditionFalse,
		},
		{
			// With -disable-approved-check an unapproved request goes on to the issuer lookup.
			name:         "approval check disabled lets an unapproved request through to the issuer",
			checkApprove: false,
			ref:          ours,
			wantErr:      true,
			wantReady:    cmapi.CertificateRequestReasonPending,
			wantStatus:   cmmeta.ConditionFalse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			cr := &cmapi.CertificateRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "ns"},
				Spec:       cmapi.CertificateRequestSpec{IssuerRef: tt.ref, IsCA: tt.isCA},
				Status:     cmapi.CertificateRequestStatus{Conditions: tt.conditions},
			}
			objs := []client.Object{cr}
			if tt.issuer != nil {
				objs = append(objs, tt.issuer)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
				WithStatusSubresource(&cmapi.CertificateRequest{}, &api.Issuer{}).Build()

			r := &CertificateRequestReconciler{
				Client:                 c,
				Scheme:                 scheme,
				Clock:                  clocktesting.NewFakeClock(now),
				CheckApprovedCondition: tt.checkApprove,
			}
			key := types.NamespacedName{Namespace: "ns", Name: "cr"}
			_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}

			got := &cmapi.CertificateRequest{}
			if err := c.Get(context.Background(), key, got); err != nil {
				t.Fatal(err)
			}
			if len(got.Status.Certificate) != 0 {
				t.Fatal("a certificate was issued")
			}
			var ready *cmapi.CertificateRequestCondition
			for i := range got.Status.Conditions {
				if got.Status.Conditions[i].Type == cmapi.CertificateRequestConditionReady {
					ready = &got.Status.Conditions[i]
				}
			}
			switch {
			case tt.wantReady == "" && ready != nil:
				t.Fatalf("unexpected Ready condition: %+v", *ready)
			case tt.wantReady != "" && ready == nil:
				t.Fatalf("missing Ready condition, want reason %q", tt.wantReady)
			case ready != nil && (ready.Reason != tt.wantReady || ready.Status != tt.wantStatus):
				t.Fatalf("Ready condition = %s/%s, want %s/%s", ready.Status, ready.Reason, tt.wantStatus, tt.wantReady)
			}
			if tt.wantReady == cmapi.CertificateRequestReasonDenied && got.Status.FailureTime == nil {
				t.Fatal("denied request has no FailureTime")
			}
		})
	}
}
