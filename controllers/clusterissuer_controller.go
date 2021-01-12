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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	provisioners "github.com/guilhem/freeipa-issuer/provisionners"
)

// ClusterIssuerReconciler reconciles a ClusterIssuer object
type ClusterIssuerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=certmanager.freeipa.org,resources=clusterissuers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=certmanager.freeipa.org,resources=clusterissuers/status,verbs=get;update;patch

func (r *ClusterIssuerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("clusterissuer", req.NamespacedName)

	iss := new(api.ClusterIssuer)
	if err := r.Client.Get(ctx, req.NamespacedName, iss); err != nil {
		log.Error(err, "failed to retrieve ClusterIssuer resource")
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

	return reconcile.Result{}, r.setStatus(ctx, iss, api.ConditionTrue, "Verified", "ClusterIssuer verified and ready to sign certificates")
}

// setStatus is a helper function to set the Issuer status condition with reason and message, and update the API.
func (r *ClusterIssuerReconciler) setStatus(ctx context.Context, iss *api.ClusterIssuer, status api.ConditionStatus, reason, message string) error {
	SetIssuerCondition(ctx, &iss.Status, api.ConditionReady, status, reason, message)

	return r.Client.Status().Update(ctx, iss)
}

func (r *ClusterIssuerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ClusterIssuer{}).
		Complete(r)
}
