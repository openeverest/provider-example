package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/openeverest/provider-example/internal/common"
)

func statefulSet(generation, observedGeneration int64, replicas, ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team-a", Generation: generation},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(replicas)},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: observedGeneration,
			ReadyReplicas:      ready,
		},
	}
}

func TestStatusFromStatefulSet(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		statefulSet *appsv1.StatefulSet
		wantPhase   corev1alpha1.InstancePhase
		wantMessage string
	}{
		"spec change not yet observed is an update in flight": {
			statefulSet: statefulSet(4, 3, 3, 3),
			wantPhase:   corev1alpha1.InstancePhaseUpdating,
			wantMessage: "Rolling out the updated configuration",
		},
		"no ready node yet is still initializing": {
			statefulSet: statefulSet(1, 1, 3, 0),
			wantPhase:   corev1alpha1.InstancePhaseInitializing,
		},
		"partially ready is an update in flight": {
			statefulSet: statefulSet(1, 1, 3, 2),
			wantPhase:   corev1alpha1.InstancePhaseUpdating,
			wantMessage: "2 of 3 nodes ready",
		},
		"every node ready is ready": {
			statefulSet: statefulSet(1, 1, 3, 3),
			wantPhase:   corev1alpha1.InstancePhaseReady,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status := statusFromStatefulSet(test.statefulSet, connectionDetails("cache", "team-a", 3))

			assert.Equal(t, test.wantPhase, status.Phase)
			if test.wantMessage != "" {
				assert.Equal(t, test.wantMessage, status.Message)
			}
		})
	}
}

func TestStatusOnlyPublishesConnectionDetailsWhenReady(t *testing.T) {
	t.Parallel()

	details := connectionDetails("cache", "team-a", 1)

	assert.True(t, statusFromStatefulSet(statefulSet(1, 1, 1, 0), details).ConnectionDetails.IsEmpty())
	assert.False(t, statusFromStatefulSet(statefulSet(1, 1, 1, 1), details).ConnectionDetails.IsEmpty())
}

func TestStatusReportsComponentProgress(t *testing.T) {
	t.Parallel()

	inProgress := statusFromStatefulSet(statefulSet(1, 1, 3, 1), connectionDetails("cache", "team-a", 3))
	require.Len(t, inProgress.Components, 1)
	assert.Equal(t, common.ComponentEngine, inProgress.Components[0].Name)
	assert.Equal(t, int32(1), inProgress.Components[0].Ready)
	assert.Equal(t, int32(3), inProgress.Components[0].Total)
	assert.Equal(t, componentStateInProgress, inProgress.Components[0].State)

	ready := statusFromStatefulSet(statefulSet(1, 1, 3, 3), connectionDetails("cache", "team-a", 3))
	require.Len(t, ready.Components, 1)
	assert.Equal(t, componentStateReady, ready.Components[0].State)
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
