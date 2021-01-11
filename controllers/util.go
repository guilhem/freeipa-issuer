package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// SetIssuerCondition will set a condition on the given Issuer.
//
// If no condition of the same type exists, the condition will be inserted with
// the LastTransitionTime set to the current time.
//
// If a condition of the same type and status already exists, the condition will
// be updated but the LastTransitionTime will no be modified.
//
// If a condition of the same type and different state already exists, the
// condition will be updated and the LastTransitionTime set to the current
// time.
func SetIssuerCondition(ctx context.Context, status *api.IssuerStatus, conditionType api.ConditionType, conditionStatus api.ConditionStatus, cl clock.Clock, reason, message string) {
	log := log.FromContext(ctx)

	now := metav1.NewTime(cl.Now())
	c := api.IssuerCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: &now,
	}

	for i, condition := range status.Conditions {
		if condition.Type != conditionType {
			continue
		}

		if condition.Status == conditionStatus {
			c.LastTransitionTime = condition.LastTransitionTime
		} else {
			log.Info("found status change for Issuer; setting lastTransitionTime",
				"condition", condition.Type,
				"old_status", condition.Status,
				"new_status", c.Status,
			)
		}

		status.Conditions[i] = c

		return
	}

	status.Conditions = append(status.Conditions, c)
}

func initSecrets(ctx context.Context, client client.Client, req ctrl.Request, user, pw api.SecretKeySelector) ([]byte, []byte, error) {

	userSecret := corev1.Secret{}
	userdNamespace := req.Namespace
	if user.Namespace != "" {
		userdNamespace = user.Namespace
	}
	userSecretNamespaceName := types.NamespacedName{
		Namespace: userdNamespace,
		Name:      user.Name,
	}

	if err := client.Get(ctx, userSecretNamespaceName, &userSecret); err != nil {
		return nil, nil, err
	}

	userData, ok := userSecret.Data[user.Key]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s does not contain key %q", userSecret.Name, user.Key)
	}

	passwordSecret := corev1.Secret{}
	passwordNamespace := req.Namespace
	if pw.Namespace != "" {
		passwordNamespace = pw.Namespace
	}
	passwordSecretNamespaceName := types.NamespacedName{
		Namespace: passwordNamespace,
		Name:      pw.Name,
	}

	if err := client.Get(ctx, passwordSecretNamespaceName, &passwordSecret); err != nil {
		return nil, nil, err
	}

	passwordData, ok := passwordSecret.Data[pw.Key]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s does not contain key %q", passwordSecret.Name, pw.Key)
	}

	return userData, passwordData, nil
}
