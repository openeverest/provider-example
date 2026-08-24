package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/internal/common"
)

// newTestContext builds a Context backed by a fake client that serves the
// Provider CR this repository ships, mirroring what `make generate` produces
// from definition/versions.yaml.
func newTestContext(t *testing.T, instance *corev1alpha1.Instance) *controller.Context {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))

	providerCR := &corev1alpha1.Provider{
		ObjectMeta: metav1.ObjectMeta{Name: common.ProviderName},
		Spec: corev1alpha1.ProviderSpec{
			ComponentTypes: map[string]corev1alpha1.ComponentType{
				common.ComponentTypeMemcached: {Versions: []corev1alpha1.ComponentVersion{
					{Version: "1.6.38", Image: "memcached:1.6.38-alpine", Default: true},
					{Version: "1.6.31", Image: "memcached:1.6.31-alpine"},
				}},
			},
			Components: map[string]corev1alpha1.Component{
				common.ComponentEngine: {Type: common.ComponentTypeMemcached},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(providerCR).Build()
	return controller.NewContext(context.Background(), client, instance, common.ProviderName)
}

// testInstance builds a minimal Instance that individual tests then adjust.
func testInstance(topology string, component corev1alpha1.ComponentSpec) *corev1alpha1.Instance {
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "team-a"},
		Spec: corev1alpha1.InstanceSpec{
			Components: map[string]corev1alpha1.ComponentSpec{common.ComponentEngine: component},
		},
	}
	if topology != "" {
		instance.Spec.Topology = &corev1alpha1.TopologySpec{Type: topology}
	}
	return instance
}

func rawParameters(t *testing.T, value any) *runtime.RawExtension {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return &runtime.RawExtension{Raw: encoded}
}

func TestResolveEngineImage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		component corev1alpha1.ComponentSpec
		want      string
	}{
		"explicit image wins over everything": {
			component: corev1alpha1.ComponentSpec{Image: "internal.registry/memcached:patched", Version: "1.6.31"},
			want:      "internal.registry/memcached:patched",
		},
		"resolved version selects its image": {
			component: corev1alpha1.ComponentSpec{Version: "1.6.31"},
			want:      "memcached:1.6.31-alpine",
		},
		"no version falls back to the catalog default": {
			component: corev1alpha1.ComponentSpec{},
			want:      "memcached:1.6.38-alpine",
		},
		"unknown version falls back to the catalog default": {
			component: corev1alpha1.ComponentSpec{Version: "0.0.1"},
			want:      "memcached:1.6.38-alpine",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			engine, err := resolveEngine(newTestContext(t, testInstance("", test.component)))
			require.NoError(t, err)
			assert.Equal(t, test.want, engine.image)
		})
	}
}

func TestResolveEngineReplicas(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		replicas *int32
		want     int32
	}{
		"defaults to a single node": {want: 1},
		"explicit replicas win":     {replicas: ptr.To(int32(5)), want: 5},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			component := corev1alpha1.ComponentSpec{Replicas: test.replicas}
			engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, component)))
			require.NoError(t, err)
			assert.Equal(t, test.want, engine.replicas)
		})
	}
}

func TestResolveEngineDerivesMemoryLimitFromResources(t *testing.T) {
	t.Parallel()

	component := corev1alpha1.ComponentSpec{
		Resources: &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
	}

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, component)))
	require.NoError(t, err)

	// 1Gi is 1024Mi, of which memcached gets 80%.
	assert.Equal(t, int64(819), engine.memoryLimitMB)
	assert.Contains(t, memcachedArgs(engine), "--memory-limit=819")
}

func TestResolveEngineWithoutMemoryLimitOmitsTheFlag(t *testing.T) {
	t.Parallel()

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{})))
	require.NoError(t, err)

	assert.Zero(t, engine.memoryLimitMB)
	assert.Equal(t, []string{"--port=11211"}, memcachedArgs(engine))
}

func TestResolveEngineDecodesComponentParameters(t *testing.T) {
	t.Parallel()

	component := corev1alpha1.ComponentSpec{
		Parameters: rawParameters(t, map[string]any{"maxConnections": 2048, "threads": 8}),
	}

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, component)))
	require.NoError(t, err)

	assert.Equal(t, []string{"--port=11211", "--conn-limit=2048", "--threads=8"}, memcachedArgs(engine))
}

func TestBuildStatefulSet(t *testing.T) {
	t.Parallel()

	component := corev1alpha1.ComponentSpec{
		Replicas: ptr.To(int32(4)),
		Resources: &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
		},
		Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{}},
	}

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, component)))
	require.NoError(t, err)

	statefulSet := buildStatefulSet(engine)

	assert.Equal(t, "cache", statefulSet.Name)
	assert.Equal(t, "team-a", statefulSet.Namespace)
	// The headless Service must match, otherwise pods get no stable DNS.
	assert.Equal(t, "cache", statefulSet.Spec.ServiceName)
	assert.Equal(t, ptr.To(int32(4)), statefulSet.Spec.Replicas)
	assert.Equal(t, engine.labels, statefulSet.Spec.Selector.MatchLabels)
	assert.Equal(t, engine.labels, statefulSet.Spec.Template.Labels)
	assert.Equal(t, component.Affinity, statefulSet.Spec.Template.Spec.Affinity)
	// memcached is in-memory only: no volumes to claim.
	assert.Empty(t, statefulSet.Spec.VolumeClaimTemplates)

	require.Len(t, statefulSet.Spec.Template.Spec.Containers, 1)
	container := statefulSet.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "memcached:1.6.38-alpine", container.Image)
	assert.Equal(t, *component.Resources, container.Resources)
	require.Len(t, container.Ports, 1)
	assert.Equal(t, int32(common.MemcachedPort), container.Ports[0].ContainerPort)
}

func TestBuildStatefulSetHardensThePodSecurityContext(t *testing.T) {
	t.Parallel()

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{})))
	require.NoError(t, err)

	podSpec := buildStatefulSet(engine).Spec.Template.Spec
	require.NotNil(t, podSpec.SecurityContext)
	assert.Equal(t, ptr.To(true), podSpec.SecurityContext.RunAsNonRoot)
	assert.Equal(t, ptr.To(int64(memcachedUID)), podSpec.SecurityContext.RunAsUser)

	container := podSpec.Containers[0]
	require.NotNil(t, container.SecurityContext)
	assert.Equal(t, ptr.To(false), container.SecurityContext.AllowPrivilegeEscalation)
	assert.Equal(t, ptr.To(true), container.SecurityContext.ReadOnlyRootFilesystem)
	assert.Equal(t, []corev1.Capability{"ALL"}, container.SecurityContext.Capabilities.Drop)
}

func TestBuildServiceIsHeadless(t *testing.T) {
	t.Parallel()

	engine, err := resolveEngine(newTestContext(t, testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{})))
	require.NoError(t, err)

	service := buildService(engine)

	assert.Equal(t, corev1.ClusterIPNone, service.Spec.ClusterIP)
	assert.Equal(t, engine.labels, service.Spec.Selector)
	require.Len(t, service.Spec.Ports, 1)
	assert.Equal(t, int32(common.MemcachedPort), service.Spec.Ports[0].Port)
}
