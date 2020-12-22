package controllers

import (
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
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
func SetIssuerCondition(iss *api.Issuer, conditionType api.ConditionType, status api.ConditionStatus, log logr.Logger, cl clock.Clock, reason, message string) {
	now := metav1.NewTime(cl.Now())
	c := api.IssuerCondition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: &now,
	}

	for i, condition := range iss.Status.Conditions {
		if condition.Type != conditionType {
			continue
		}

		if condition.Status == status {
			c.LastTransitionTime = condition.LastTransitionTime
		} else {
			log.Info("found status change for Issuer; setting lastTransitionTime",
				"condition", condition.Type,
				"old_status", condition.Status,
				"new_status", c.Status,
			)
		}

		iss.Status.Conditions[i] = c

		return
	}

	iss.Status.Conditions = append(iss.Status.Conditions, c)
}
