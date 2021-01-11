/*
Copyright 2020 Guilhem Lettron.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	provisioners "github.com/guilhem/freeipa-issuer/provisionners"
)

// IssuerReconciler reconciles a Issuer object
type IssuerReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Clock clock.Clock
}

// +kubebuilder:rbac:groups=certmanager.freeipa.org,resources=issuers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=certmanager.freeipa.org,resources=issuers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;create;update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *IssuerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx).WithValues("issuer", req.NamespacedName)

	iss := new(api.Issuer)
	if err := r.Client.Get(ctx, req.NamespacedName, iss); err != nil {
		log.Error(err, "failed to retrieve Issuer resource")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	user, password, err := initSecrets(ctx, r.Client, req, *iss.Spec.User, *iss.Spec.Password)

	if err != nil {
		log.Error(err, "failed to retieve Issuer auth secret")

		if apierrors.IsNotFound(err) {
			_ = r.setStatus(ctx, iss, api.ConditionFalse, "NotFound", fmt.Sprintf("Failed to retrieve auth secret: %v", err))
		} else {
			_ = r.setStatus(ctx, iss, api.ConditionFalse, "Error", fmt.Sprintf("Failed to retrieve auth secret: %v", err))
		}

		return reconcile.Result{}, err
	}

	// Initialize and store the provisioner
	p, err := provisioners.New(req.NamespacedName, &iss.Spec, string(user), string(password), iss.Spec.Insecure)
	if err != nil {
		log.Error(err, "failed to create provisioner")
		_ = r.setStatus(ctx, iss, api.ConditionFalse, "Error", "Failed initialize provisioner")
		return reconcile.Result{}, err
	}

	provisioners.Store(req.NamespacedName, p)

	return reconcile.Result{}, r.setStatus(ctx, iss, api.ConditionTrue, "Verified", "Issuer verified and ready to sign certificates")
}

// setStatus is a helper function to set the Issuer status condition with reason and message, and update the API.
func (r *IssuerReconciler) setStatus(ctx context.Context, iss *api.Issuer, status api.ConditionStatus, reason, message string) error {
	SetIssuerCondition(ctx, &iss.Status, api.ConditionReady, status, r.Clock, reason, message)

	return r.Client.Status().Update(ctx, iss)
}

func (r *IssuerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.Issuer{}).
		Complete(r)
}
