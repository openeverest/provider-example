package provider

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/definition/components"
	"github.com/openeverest/provider-example/internal/common"
)

// memcachedUID is the uid the official memcached images run as. It is set
// explicitly because runAsNonRoot cannot be enforced when an image declares
// its user by name.
const memcachedUID = 11211

// memoryLimitPercent is the share of the container memory limit handed to
// memcached's own cache. The remainder covers per-connection buffers and
// slab overhead, which live outside that budget and would otherwise push the
// container over its limit and get it OOM-killed.
const memoryLimitPercent = 80

// engineSpec is everything needed to render the workload, resolved from the
// Instance and the Provider CR.
//
// Sync is split in two on purpose: resolveEngine talks to the cluster, while
// buildStatefulSet and buildService are pure functions of this struct. That is
// what makes the interesting half of the provider testable without a cluster.
type engineSpec struct {
	name      string
	namespace string
	labels    map[string]string

	image    string
	replicas int32

	resources corev1.ResourceRequirements
	// memoryLimitMB is derived from resources, not configured directly. Zero
	// means no limit was set, so memcached keeps its own default.
	memoryLimitMB  int64
	maxConnections *int32
	threads        *int32

	affinity *corev1.Affinity
}

// sync makes the cluster match the Instance spec.
//
// It runs on every reconciliation, so it must be idempotent: build the objects
// the spec describes and apply them. Context.Apply creates or updates, and
// sets a controller reference to the Instance, which is what makes Cleanup a
// no-op and lets the runtime watch these objects as owned.
func sync(c *controller.Context) error {
	engine, err := resolveEngine(c)
	if err != nil {
		return err
	}

	// The Service is applied first so pod DNS resolves as soon as pods appear.
	if err := c.Apply(buildService(engine)); err != nil {
		return fmt.Errorf("apply service: %w", err)
	}
	if err := c.Apply(buildStatefulSet(engine)); err != nil {
		return fmt.Errorf("apply statefulset: %w", err)
	}
	return nil
}

// resolveEngine reads the Instance and the Provider CR and produces a fully
// defaulted description of the workload.
func resolveEngine(c *controller.Context) (*engineSpec, error) {
	instance := c.Instance()
	component := instance.Spec.Components[common.ComponentEngine]

	image, err := resolveImage(c, component)
	if err != nil {
		return nil, err
	}

	engine := &engineSpec{
		name:      c.Name(),
		namespace: c.Namespace(),
		labels:    selectorLabels(c.Name()),
		image:     image,
		replicas:  resolveReplicas(component.Replicas),
		affinity:  component.Affinity,
	}

	if component.Resources != nil {
		engine.resources = *component.Resources
		engine.memoryLimitMB = memcachedMemoryLimitMB(*component.Resources)
	}

	// Component parameters configure the engine process.
	var params components.MemcachedParameters
	if c.TryDecodeComponentParameters(component, &params) {
		engine.maxConnections = params.MaxConnections
		engine.threads = params.Threads
	}

	return engine, nil
}

// resolveImage picks the container image for the engine component.
//
// Precedence is explicit image, then the version the runtime resolved from the
// Instance's version bundle, then the component type's default version. The
// bundle itself is already resolved by the time Sync runs, so this never has
// to look one up.
func resolveImage(c *controller.Context, component corev1alpha1.ComponentSpec) (string, error) {
	if component.Image != "" {
		return component.Image, nil
	}

	providerSpec, err := c.ProviderSpec()
	if err != nil {
		return "", fmt.Errorf("get provider spec: %w", err)
	}

	if component.Version != "" {
		if image := controller.GetImageForVersion(providerSpec, common.ComponentEngine, component.Version); image != "" {
			return image, nil
		}
	}
	image := controller.GetDefaultImageForComponent(providerSpec, common.ComponentEngine)
	if image == "" {
		return "", fmt.Errorf("no image found for component %q", common.ComponentEngine)
	}
	return image, nil
}

// topologyType returns the requested topology, defaulting to the only one this
// provider offers.
func topologyType(instance *corev1alpha1.Instance) string {
	if instance.Spec.Topology == nil || instance.Spec.Topology.Type == "" {
		return common.TopologyPool
	}
	return instance.Spec.Topology.Type
}

// resolveReplicas defaults the node count. The `defaults` block in
// topology.yaml is a documentation and UI hint — nothing defaults component
// fields at runtime.
func resolveReplicas(requested *int32) int32 {
	if requested != nil {
		return *requested
	}
	return 1
}

// memcachedMemoryLimitMB converts the container memory limit into memcached's
// --memory-limit, which is expressed in megabytes.
func memcachedMemoryLimitMB(resources corev1.ResourceRequirements) int64 {
	limit, ok := resources.Limits[corev1.ResourceMemory]
	if !ok {
		return 0
	}
	return limit.Value() / (1024 * 1024) * memoryLimitPercent / 100
}

// selectorLabels identify the pods of one Instance. They are the StatefulSet
// selector, which Kubernetes treats as immutable, so they are kept separate
// from the descriptive labels Context.ObjectMeta adds.
func selectorLabels(instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     common.ComponentTypeMemcached,
		"app.kubernetes.io/instance": instanceName,
	}
}

// buildService renders the headless Service that gives every node a stable DNS
// name (<instance>-<ordinal>.<instance>.<namespace>.svc). memcached clients
// shard keys across nodes themselves, so they need to address each node, not a
// load-balanced VIP.
func buildService(engine *engineSpec) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      engine.name,
			Namespace: engine.namespace,
			Labels:    engine.labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  engine.labels,
			Ports: []corev1.ServicePort{{
				Name:       common.ComponentTypeMemcached,
				Port:       common.MemcachedPort,
				TargetPort: intstr.FromInt32(common.MemcachedPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// buildStatefulSet renders the workload.
//
// A StatefulSet rather than a Deployment because pool members are addressed
// individually by clients, and stable ordinal names are what makes that
// possible. There are no volumeClaimTemplates: memcached is an in-memory
// cache, so this provider does not use the Instance's storage fields.
func buildStatefulSet(engine *engineSpec) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      engine.name,
			Namespace: engine.namespace,
			Labels:    engine.labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: engine.name,
			Replicas:    ptr.To(engine.replicas),
			Selector:    &metav1.LabelSelector{MatchLabels: engine.labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: engine.labels},
				Spec: corev1.PodSpec{
					Affinity: engine.affinity,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						RunAsUser:      ptr.To(int64(memcachedUID)),
						RunAsGroup:     ptr.To(int64(memcachedUID)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:      common.ComponentTypeMemcached,
						Image:     engine.image,
						Args:      memcachedArgs(engine),
						Resources: engine.resources,
						Ports: []corev1.ContainerPort{{
							Name:          common.ComponentTypeMemcached,
							ContainerPort: common.MemcachedPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(common.MemcachedPort)},
							},
							InitialDelaySeconds: 2,
							PeriodSeconds:       5,
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
}

// memcachedArgs turns the resolved spec into memcached's command line. Long
// option names are used for readability; they are the same settings as -m, -c
// and -t.
func memcachedArgs(engine *engineSpec) []string {
	args := []string{fmt.Sprintf("--port=%d", common.MemcachedPort)}
	if engine.memoryLimitMB > 0 {
		args = append(args, fmt.Sprintf("--memory-limit=%d", engine.memoryLimitMB))
	}
	if engine.maxConnections != nil {
		args = append(args, fmt.Sprintf("--conn-limit=%d", *engine.maxConnections))
	}
	if engine.threads != nil {
		args = append(args, fmt.Sprintf("--threads=%d", *engine.threads))
	}
	return args
}
