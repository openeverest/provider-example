package provider

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// openShiftSecurityGroup is the API group that serves SecurityContextConstraints.
// Only OpenShift (and OKD) serve it, which makes it a cheap platform check.
const openShiftSecurityGroup = "security.openshift.io"

// DetectOpenShift reports whether the cluster is OpenShift.
//
// OpenShift's default restricted-v2 SCC gives each namespace its own UID/GID
// range and rejects pods that pin IDs outside it, so workloads must leave
// runAsUser, runAsGroup and fsGroup unset there and let the SCC assign them.
// Other clusters have no such range, so images that declare their user by
// name need a numeric runAsUser for runAsNonRoot to be enforceable.
func DetectOpenShift(cfg *rest.Config) (bool, error) {
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, fmt.Errorf("create discovery client: %w", err)
	}
	groups, err := client.ServerGroups()
	if err != nil {
		return false, fmt.Errorf("list API groups: %w", err)
	}
	return slices.ContainsFunc(groups.Groups, func(g metav1.APIGroup) bool {
		return g.Name == openShiftSecurityGroup
	}), nil
}
