package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/openeverest/provider-example/internal/common"
)

func TestValidateAcceptsSupportedSpecs(t *testing.T) {
	t.Parallel()

	tests := map[string]*corev1alpha1.Instance{
		"defaults":           testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{}),
		"a single node":      testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{Replicas: ptr.To(int32(1))}),
		"several nodes":      testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{Replicas: ptr.To(int32(5))}),
		"no topology at all": testInstance("", corev1alpha1.ComponentSpec{}),
	}

	for name, instance := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validate(newTestContext(t, instance)))
		})
	}
}

func TestValidateRejectsUnsupportedSpecs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		instance    *corev1alpha1.Instance
		wantMessage string
	}{
		"unknown topology": {
			instance:    testInstance("sharded", corev1alpha1.ComponentSpec{}),
			wantMessage: `unknown topology "sharded"`,
		},
		"missing engine component": {
			instance: &corev1alpha1.Instance{
				Spec: corev1alpha1.InstanceSpec{Topology: &corev1alpha1.TopologySpec{Type: common.TopologyPool}},
			},
			wantMessage: `component "engine" is required`,
		},
		"zero nodes": {
			instance:    testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{Replicas: ptr.To(int32(0))}),
			wantMessage: "needs at least 1 node",
		},
		"more nodes than supported": {
			instance:    testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{Replicas: ptr.To(int32(20))}),
			wantMessage: "a pool supports at most 9 nodes",
		},
		"connection limit out of range": {
			instance: testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{
				Parameters: &runtime.RawExtension{Raw: []byte(`{"maxConnections":70000}`)},
			}),
			wantMessage: "maxConnections must be between 1 and 65535",
		},
		"thread count out of range": {
			instance: testInstance(common.TopologyPool, corev1alpha1.ComponentSpec{
				Parameters: &runtime.RawExtension{Raw: []byte(`{"threads":0}`)},
			}),
			wantMessage: "threads must be between 1 and 64",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validate(newTestContext(t, test.instance))
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantMessage)
		})
	}
}
