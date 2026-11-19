package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/internal/common"
)

// statefulSet returns the StatefulSet of the "cache" Instance, its latest
// spec seen by the StatefulSet controller, which reports the given pod
// counts.
func statefulSet(replicas, updated, ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team-a", Generation: 2},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 2,
			Replicas:           replicas,
			UpdatedReplicas:    updated,
			ReadyReplicas:      ready,
		},
	}
}

// memcachedPod returns a node of the "cache" Instance with the given status.
func memcachedPod(name string, container corev1.ContainerStatus) *corev1.Pod {
	container.Name = common.ComponentTypeMemcached
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team-a", Labels: selectorLabels("cache")},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{container}},
	}
}

// restartingAfter is a container waiting to be restarted after it ended for
// reason.
func restartingAfter(reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 137}},
	}
}

func TestObserve(t *testing.T) {
	t.Parallel()

	notSeen := statefulSet(3, 3, 3)
	notSeen.Generation = 3

	tests := map[string]struct {
		statefulSet *appsv1.StatefulSet
		pods        []corev1.Pod
		desired     int32

		wantConverged       bool
		wantMessage         string
		wantAvailable       controller.Availability
		wantAvailableReason string
		wantProblems        []controller.Problem
	}{
		"a spec the StatefulSet controller has not seen is not converged": {
			statefulSet:   notSeen,
			desired:       3,
			wantMessage:   "Waiting for the StatefulSet controller to pick up the latest spec",
			wantAvailable: controller.Available,
		},
		"a new pool with no node ready is unavailable": {
			statefulSet:         statefulSet(1, 1, 0),
			desired:             2,
			wantMessage:         "Waiting for nodes to become ready (0 of 2 ready)",
			wantAvailable:       controller.Unavailable,
			wantAvailableReason: reasonNoNodeReady,
		},
		"a rolling restart stays available": {
			statefulSet:   statefulSet(3, 1, 2),
			desired:       3,
			wantMessage:   "Waiting for nodes to restart with the latest spec (1 of 3 updated)",
			wantAvailable: controller.Available,
		},
		"a node replaced by a rolling restart counts against the whole pool": {
			statefulSet:   statefulSet(2, 0, 2),
			desired:       3,
			wantMessage:   "Waiting for nodes to restart with the latest spec (0 of 3 updated)",
			wantAvailable: controller.Available,
		},
		"a scale-out stays available": {
			statefulSet:   statefulSet(2, 2, 2),
			desired:       3,
			wantMessage:   "Waiting for nodes to become ready (2 of 3 ready)",
			wantAvailable: controller.Available,
		},
		"a scale-in waits for the extra nodes to stop": {
			statefulSet:   statefulSet(3, 3, 3),
			desired:       2,
			wantMessage:   "Waiting for the pool to shrink to 2 nodes (it has 3)",
			wantAvailable: controller.Available,
		},
		"every node ready on the latest spec is converged": {
			statefulSet:   statefulSet(3, 3, 3),
			desired:       3,
			wantConverged: true,
			wantAvailable: controller.Available,
		},
		"a node killed for running out of memory is a problem": {
			statefulSet: statefulSet(3, 3, 1),
			pods: []corev1.Pod{
				*memcachedPod("cache-2", restartingAfter("OOMKilled")),
				*memcachedPod("cache-0", corev1.ContainerStatus{Ready: true}),
				*memcachedPod("cache-1", corev1.ContainerStatus{
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
				}),
			},
			desired:       3,
			wantMessage:   "Waiting for nodes to become ready (1 of 3 ready)",
			wantAvailable: controller.Available,
			wantProblems: []controller.Problem{{
				Reason:  reasonOOMKilled,
				Message: "The kernel killed memcached on cache-1, cache-2 for exceeding its memory limit. Raise the engine's memory limit or lower maxConnections.",
			}},
		},
		"a node back up after running out of memory is no problem": {
			statefulSet: statefulSet(3, 3, 3),
			pods: []corev1.Pod{*memcachedPod("cache-1", corev1.ContainerStatus{
				Ready:                true,
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}},
			})},
			desired:       3,
			wantConverged: true,
			wantAvailable: controller.Available,
		},
		"a node down for another reason is left to the runtime": {
			statefulSet:   statefulSet(3, 3, 2),
			pods:          []corev1.Pod{*memcachedPod("cache-1", restartingAfter("Error"))},
			desired:       3,
			wantMessage:   "Waiting for nodes to become ready (2 of 3 ready)",
			wantAvailable: controller.Available,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status := observe(test.statefulSet, test.pods, test.desired)

			assert.Equal(t, test.wantConverged, status.Converged)
			assert.Equal(t, test.wantMessage, status.Message)
			assert.Equal(t, test.wantAvailable, status.Available)
			assert.Equal(t, test.wantAvailableReason, status.AvailableReason)
			assert.Equal(t, test.wantProblems, status.Problems)
			// Nothing here is a long operation: there are no restores and
			// no volumes to resize.
			assert.Empty(t, status.Operations)
		})
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	threeNodes := corev1alpha1.ComponentSpec{Replicas: ptr.To(int32(3))}

	t.Run("a missing StatefulSet is waited for", func(t *testing.T) {
		t.Parallel()

		got, err := status(newTestContext(t, testInstance(common.TopologyPool, threeNodes)))
		require.NoError(t, err)

		assert.False(t, got.Converged)
		assert.Equal(t, "Waiting for the memcached StatefulSet to be created", got.Message)
		assert.Equal(t, controller.Unavailable, got.Available)
		assert.True(t, got.ConnectionDetails.IsEmpty())
	})

	t.Run("connection details are published once a node is ready, before the pool has converged", func(t *testing.T) {
		t.Parallel()

		got, err := status(newTestContext(t, testInstance(common.TopologyPool, threeNodes), statefulSet(2, 2, 2)))
		require.NoError(t, err)

		assert.False(t, got.Converged)
		assert.Equal(t, controller.Available, got.Available)
		// The node list follows the spec, including the node still starting.
		assert.Equal(t, connectionDetails("cache", "team-a", 3), got.ConnectionDetails)
	})

	t.Run("no connection details while no node is ready", func(t *testing.T) {
		t.Parallel()

		got, err := status(newTestContext(t, testInstance(common.TopologyPool, threeNodes), statefulSet(1, 1, 0)))
		require.NoError(t, err)

		assert.Equal(t, controller.Unavailable, got.Available)
		assert.True(t, got.ConnectionDetails.IsEmpty())
	})

	t.Run("the pods of a node that is not ready are read to name the problem", func(t *testing.T) {
		t.Parallel()

		c := newTestContext(t, testInstance(common.TopologyPool, threeNodes),
			statefulSet(3, 3, 2), memcachedPod("cache-1", restartingAfter("OOMKilled")))

		got, err := status(c)
		require.NoError(t, err)

		require.Len(t, got.Problems, 1)
		assert.Equal(t, reasonOOMKilled, got.Problems[0].Reason)
		assert.False(t, got.Problems[0].Terminal)
	})

	t.Run("a pool running the spec is converged", func(t *testing.T) {
		t.Parallel()

		got, err := status(newTestContext(t, testInstance(common.TopologyPool, threeNodes), statefulSet(3, 3, 3)))
		require.NoError(t, err)

		assert.True(t, got.Converged)
		assert.Equal(t, controller.Available, got.Available)
		assert.Empty(t, got.Problems)
		assert.False(t, got.ConnectionDetails.IsEmpty())
	})
}

func TestStatusReportsDesiredReplicas(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		replicas *int32
		objects  []client.Object
		want     int32
	}{
		"defaults to a single node, as Sync does": {
			objects: []client.Object{statefulSet(1, 1, 1)},
			want:    1,
		},
		"the spec's node count, not what the StatefulSet has yet": {
			replicas: ptr.To(int32(3)),
			objects:  []client.Object{statefulSet(2, 2, 2)},
			want:     3,
		},
		"also while the StatefulSet is missing": {
			replicas: ptr.To(int32(3)),
			want:     3,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			instance := testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{Replicas: test.replicas})
			got, err := status(newTestContext(t, instance, test.objects...))
			require.NoError(t, err)

			assert.Equal(t, []controller.ComponentReplicas{{Component: common.ComponentEngine, Desired: test.want}}, got.DesiredReplicas)
		})
	}
}

func TestConnectionDetailsAddressEveryNode(t *testing.T) {
	t.Parallel()

	details := connectionDetails("cache", "team-a", 3)

	assert.Equal(t, "memcached", details.Type)
	assert.Equal(t, common.ProviderName, details.Provider)
	assert.Equal(t, "cache.team-a.svc", details.Host)
	assert.Equal(t, "11211", details.Port)
	assert.Equal(t, "memcached://cache.team-a.svc:11211", details.URI)
	// Clients shard across nodes, so each one has to be addressable.
	assert.Equal(t,
		"cache-0.cache.team-a.svc:11211,cache-1.cache.team-a.svc:11211,cache-2.cache.team-a.svc:11211",
		details.AdditionalProperties["nodes"])
}
