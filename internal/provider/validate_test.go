package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-example/internal/common"
)

const (
	providerSpecPath = "../../charts/provider-example/generated/provider-spec.yaml"
	poolFixture      = "testdata/validate/pool.yaml"
)

// withEngine applies mutate to the engine component of the Instance.
func withEngine(mutate func(*corev1alpha1.ComponentSpec)) func(*corev1alpha1.Instance) {
	return func(in *corev1alpha1.Instance) {
		c := in.Spec.Components[common.ComponentEngine]
		mutate(&c)
		in.Spec.Components[common.ComponentEngine] = c
	}
}

func parameters(raw string) *runtime.RawExtension {
	return &runtime.RawExtension{Raw: []byte(raw)}
}

// TestValidate runs the checks the runtime runs before every Sync: the
// schemas and catalog the provider publishes, then Validate.
func TestValidate(t *testing.T) {
	t.Parallel()
	v := validationtest.New(t, New(), providerSpecPath)

	tests := []struct {
		name   string
		mutate func(*corev1alpha1.Instance)
		want   []string
	}{
		{name: "valid pool"},
		{
			name: "smallest Instance",
			mutate: func(in *corev1alpha1.Instance) {
				in.Spec.Version = ""
				in.Spec.Topology = nil
				in.Spec.Components[common.ComponentEngine] = corev1alpha1.ComponentSpec{}
			},
		},
		{
			name:   "largest pool",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) { c.Replicas = ptr.To(int32(maxPoolNodes)) }),
		},
		{
			name:   "unknown version",
			mutate: func(in *corev1alpha1.Instance) { in.Spec.Version = "1.4.0" },
			want:   []string{"spec.version"},
		},
		{
			name:   "unknown topology",
			mutate: func(in *corev1alpha1.Instance) { in.Spec.Topology.Type = "sharded" },
			want:   []string{"spec.topology.type"},
		},
		{
			name:   "engine type other than memcached",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) { c.Type = "redis" }),
			want:   []string{"spec.components[engine].type"},
		},
		{
			name: "missing engine without a topology type",
			mutate: func(in *corev1alpha1.Instance) {
				in.Spec.Topology = nil
				delete(in.Spec.Components, common.ComponentEngine)
			},
			want: []string{"spec.components[engine]"},
		},
		{
			name:   "zero nodes",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) { c.Replicas = ptr.To(int32(0)) }),
			want:   []string{"spec.components[engine].replicas"},
		},
		{
			name:   "more nodes than a pool supports",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) { c.Replicas = ptr.To(int32(20)) }),
			want:   []string{"spec.components[engine].replicas"},
		},
		{
			name: "connection limit out of range",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) {
				c.Parameters = parameters(`{"maxConnections":70000}`)
			}),
			want: []string{"spec.components[engine].parameters.maxConnections"},
		},
		{
			name: "every rule broken at once",
			mutate: withEngine(func(c *corev1alpha1.ComponentSpec) {
				c.Replicas = ptr.To(int32(0))
				c.Parameters = parameters(`{"maxConnections":0,"threads":65}`)
			}),
			want: []string{
				"spec.components[engine].replicas",
				"spec.components[engine].parameters.maxConnections",
				"spec.components[engine].parameters.threads",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := validationtest.Load(t, poolFixture)
			if tt.mutate != nil {
				tt.mutate(in)
			}

			res := v.Run(in, nil)

			var got []string
			for _, err := range res.Errors {
				got = append(got, err.Field)
			}
			assert.Equal(t, tt.want, got, "errors: %v", res.Errors)
		})
	}
}
