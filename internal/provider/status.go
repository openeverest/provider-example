package provider

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/internal/common"
)

// Reasons are the CamelCase words users read as the Instance's status and
// search for, so each one names a single cause.
const (
	// reasonNoNodeReady says why clients cannot connect.
	reasonNoNodeReady = "NoNodeReady"
	// reasonOOMKilled names a problem: the kernel killed memcached for using
	// more memory than its container may.
	reasonOOMKilled = "OOMKilled"
)

// status reports facts about the workload: whether it runs what the Instance
// asks for, whether clients can connect, and what is wrong.
//
// It does not choose a phase. The runtime derives the Instance's phase and
// conditions (Ready, Available, Reconciling, Stalled, ...) from these facts,
// the same way for every provider, and publishes the connection details in a
// Secret. It also reports on the pods itself, so pods that crash, cannot pull
// their image or cannot be scheduled need no code here.
func status(c *controller.Context) (controller.Status, error) {
	desired := resolveReplicas(c.Instance().Spec.Components[common.ComponentEngine].Replicas)

	statefulSet, err := uncachedStatefulSet(c)
	if apierrors.IsNotFound(err) {
		// Sync applies the StatefulSet before Status runs, so it is only
		// missing when it was deleted since. The next Sync recreates it.
		return controller.Progressing("Waiting for the memcached StatefulSet to be created").
			WithUnavailable(reasonNoNodeReady, "No memcached node is running"), nil
	}
	if err != nil {
		return controller.Status{}, err
	}

	// Pods only explain a node that is not ready, so they are not read while
	// every node is.
	var pods []corev1.Pod
	if statefulSet.Status.ReadyReplicas < desired {
		if pods, err = uncachedPods(c); err != nil {
			return controller.Status{}, err
		}
	}

	status := observe(statefulSet, pods, desired)

	// Connection details: published as soon as clients can connect, not only
	// once converged, so that whoever is told the cache is available finds
	// where to connect, and the node list follows a scale-out still rolling.
	if status.Available == controller.Available {
		status.ConnectionDetails = connectionDetails(c.Name(), c.Namespace(), desired)
	}
	return status, nil
}

// observe turns the StatefulSet and its pods into facts. It reads nothing
// from the cluster, so every state can be unit tested.
func observe(statefulSet *appsv1.StatefulSet, pods []corev1.Pod, desired int32) controller.Status {
	// Converged: every node runs the current spec and is ready. Until then
	// the message says what is being waited for; it becomes the message of
	// the Instance's Ready condition.
	var status controller.Status
	if wait := waitingFor(statefulSet, desired); wait != "" {
		status = controller.Progressing(wait)
	} else {
		status = controller.Ready()
	}

	// Available: whether clients can use the cache right now, which is not
	// the same as converged. memcached nodes are independent, each holding
	// its own share of the keys, so the pool serves as long as one node is
	// ready, through a rolling restart or a scale-out. A node is ready once
	// it passes its readiness probe, the same signal that publishes its
	// address in the headless Service's DNS.
	if statefulSet.Status.ReadyReplicas > 0 {
		status = status.WithAvailable(controller.Available)
	} else {
		status = status.WithUnavailable(reasonNoNodeReady, "No memcached node is ready")
	}

	// Problems: what is wrong that this provider can name. The runtime
	// reports a pod that keeps crashing, whatever the cause; only the
	// provider knows what an out-of-memory kill means here. Sync gives
	// memcached part of the container's memory limit and leaves the rest for
	// connection buffers, so a node killed for running out of memory means
	// that headroom is too small for the load, and naming it from the first
	// kill tells the user what to change. It is not terminal: the node
	// restarts, and the problem clears once it stays up.
	if nodes := oomKilledNodes(pods); len(nodes) > 0 {
		status = status.WithProblem(reasonOOMKilled, fmt.Sprintf(
			"The kernel killed memcached on %s for exceeding its memory limit. Raise the engine's memory limit or lower maxConnections.",
			strings.Join(nodes, ", ")))
	}

	return status
}

// waitingFor says what keeps the pool from running the current spec, or ""
// when nothing does.
func waitingFor(statefulSet *appsv1.StatefulSet, desired int32) string {
	st := statefulSet.Status
	switch {
	case st.ObservedGeneration < statefulSet.Generation:
		// Until then every count below describes the previous spec.
		return "Waiting for the StatefulSet controller to pick up the latest spec"
	case st.UpdatedReplicas < st.Replicas:
		// Pods created for a scale-out start on the latest spec, so only
		// a restart leaves some behind.
		return fmt.Sprintf("Waiting for nodes to restart with the latest spec (%d of %d updated)", st.UpdatedReplicas, desired)
	case st.ReadyReplicas < desired:
		return fmt.Sprintf("Waiting for nodes to become ready (%d of %d ready)", st.ReadyReplicas, desired)
	case st.Replicas > desired:
		return fmt.Sprintf("Waiting for the pool to shrink to %d nodes (it has %d)", desired, st.Replicas)
	}
	return ""
}

// oomKilledNodes returns the nodes that are not ready because the kernel
// killed memcached for exceeding its memory limit.
func oomKilledNodes(pods []corev1.Pod) []string {
	var nodes []string
	for _, pod := range pods {
		for _, container := range pod.Status.ContainerStatuses {
			if container.Ready {
				continue
			}
			// A container waiting to be restarted keeps how it ended in
			// lastState.
			ended := container.State.Terminated
			if ended == nil {
				ended = container.LastTerminationState.Terminated
			}
			if ended != nil && ended.Reason == "OOMKilled" {
				nodes = append(nodes, pod.Name)
			}
		}
	}
	slices.Sort(nodes)
	return nodes
}

// uncachedStatefulSet reads the StatefulSet from the API server instead of
// the informer cache.
//
// Sync has just applied it, and Context.Get, which reads typed objects from
// the cache, may still return the copy from before that: its generation and
// status would agree, every node would be ready, and the Instance would read
// as converged on a change the StatefulSet controller has not even seen. The
// manager's client does not cache unstructured objects, so reading one goes
// to the API server.
func uncachedStatefulSet(c *controller.Context) (*appsv1.StatefulSet, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("StatefulSet"))
	if err := c.Get(obj, c.Name()); err != nil {
		return nil, fmt.Errorf("get statefulset: %w", err)
	}

	statefulSet := &appsv1.StatefulSet{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, statefulSet); err != nil {
		return nil, fmt.Errorf("decode statefulset: %w", err)
	}
	return statefulSet, nil
}

// uncachedPods lists the memcached pods of the Instance from the API server.
// Listing them through the cache would keep every pod of the Kubernetes
// cluster in memory, for a read that is only needed while a node is down.
func uncachedPods(c *controller.Context) ([]corev1.Pod, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	if err := c.List(list, client.MatchingLabels(selectorLabels(c.Name()))); err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}

	pods := make([]corev1.Pod, len(list.Items))
	for i := range list.Items {
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[i].Object, &pods[i]); err != nil {
			return nil, fmt.Errorf("decode pod: %w", err)
		}
	}
	return pods, nil
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
