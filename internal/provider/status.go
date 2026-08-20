package provider

import (
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/internal/common"
)

// Component states reported alongside the Instance phase.
const (
	componentStateReady      = "Ready"
	componentStateInProgress = "InProgress"
)

// status reports what the workload actually looks like.
//
// Sync says what should exist; this says what does. The runtime writes the
// returned phase onto the Instance and, when the phase is Ready, persists the
// connection details into a Secret the API server serves to clients.
func status(c *controller.Context) (controller.Status, error) {
	statefulSet := &appsv1.StatefulSet{}
	if err := c.Get(statefulSet, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			// Not an error: Sync has run but the cache has not caught up yet.
			return controller.Provisioning("Waiting for the memcached StatefulSet to be created"), nil
		}
		return controller.Status{}, fmt.Errorf("get statefulset: %w", err)
	}

	return statusFromStatefulSet(statefulSet, connectionDetails(c.Name(), c.Namespace(), desiredReplicas(statefulSet))), nil
}

// statusFromStatefulSet maps StatefulSet status onto an Instance phase.
//
// The phases are not decoration: the UI, the CLI and everything waiting on an
// Instance read them, so each one has to mean something specific.
func statusFromStatefulSet(statefulSet *appsv1.StatefulSet, details controller.ConnectionDetails) controller.Status {
	desired := desiredReplicas(statefulSet)
	ready := statefulSet.Status.ReadyReplicas

	var status controller.Status
	switch {
	case statefulSet.Status.ObservedGeneration < statefulSet.Generation:
		// The spec changed and the StatefulSet controller has not acted yet.
		status = controller.Updating("Rolling out the updated configuration")
	case ready == 0:
		// The workload exists but nothing serves traffic yet.
		status = controller.Initializing("Waiting for the first memcached node to become ready")
	case ready < desired:
		// Some nodes serve traffic: a scale-out or a rolling update.
		status = controller.Updating(fmt.Sprintf("%d of %d nodes ready", ready, desired))
	default:
		status = controller.ReadyWithConnectionDetails(details)
	}

	state := componentStateInProgress
	if ready == desired {
		state = componentStateReady
	}
	status.Components = []controller.ComponentStatus{{
		Name:  common.ComponentEngine,
		Ready: ready,
		Total: desired,
		State: state,
	}}

	return status
}

// connectionDetails describes how to reach the cache.
//
// The well-known fields are filled in so the API server can serve them without
// knowing anything about memcached. There is no username or password because
// memcached has no authentication — a real provider would read the credentials
// it generated and set Username and Password here.
func connectionDetails(name, namespace string, replicas int32) controller.ConnectionDetails {
	host := fmt.Sprintf("%s.%s.svc", name, namespace)
	port := strconv.Itoa(common.MemcachedPort)

	// Clients shard keys across nodes themselves, so the individual node
	// addresses matter as much as the service name. AdditionalProperties is
	// how a provider passes through anything the well-known fields do not
	// cover; every entry becomes a key in the connection Secret.
	nodes := make([]string, 0, replicas)
	for ordinal := range replicas {
		nodes = append(nodes, fmt.Sprintf("%s-%d.%s:%s", name, ordinal, host, port))
	}

	return controller.ConnectionDetails{
		Type:     common.ComponentTypeMemcached,
		Provider: common.ProviderName,
		Host:     host,
		Port:     port,
		URI:      fmt.Sprintf("memcached://%s:%s", host, port),
		AdditionalProperties: map[string]string{
			"nodes": strings.Join(nodes, ","),
		},
	}
}

func desiredReplicas(statefulSet *appsv1.StatefulSet) int32 {
	if statefulSet.Spec.Replicas == nil {
		return 1
	}
	return *statefulSet.Spec.Replicas
}
