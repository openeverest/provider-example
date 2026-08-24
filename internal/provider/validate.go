package provider

import (
	"fmt"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/definition/components"
	"github.com/openeverest/provider-example/internal/common"
)

// Bounds mirrored from the validation rules in definition/topologies/pool.
// The form stops most of these client-side; this is what stops the rest,
// because an Instance can also arrive through kubectl or GitOps.
const (
	maxPoolNodes       = 9
	maxConnectionCount = 65535
	maxThreadCount     = 64
)

// validate rejects Instance specs this provider cannot serve.
//
// A spec submitted through the API or the UI is rejected before the Instance
// exists. One applied with kubectl is persisted first and then fails here, so
// the message has to read well both in a form and in `kubectl describe`.
// Anything that would reconcile forever instead of failing loudly belongs here.
func validate(c *controller.Context) error {
	instance := c.Instance()

	topology := topologyType(instance)
	if topology != common.TopologyPool {
		return fmt.Errorf("unknown topology %q: this provider only offers %q", topology, common.TopologyPool)
	}

	component, ok := instance.Spec.Components[common.ComponentEngine]
	if !ok {
		return fmt.Errorf("component %q is required", common.ComponentEngine)
	}

	if component.Replicas != nil {
		replicas := *component.Replicas
		switch {
		case replicas < 1:
			return fmt.Errorf("component %q needs at least 1 node, got %d", common.ComponentEngine, replicas)
		case replicas > maxPoolNodes:
			return fmt.Errorf("a pool supports at most %d nodes, got %d", maxPoolNodes, replicas)
		}
	}

	var params components.MemcachedParameters
	if c.TryDecodeComponentParameters(component, &params) {
		if params.MaxConnections != nil && (*params.MaxConnections < 1 || *params.MaxConnections > maxConnectionCount) {
			return fmt.Errorf("maxConnections must be between 1 and %d, got %d", maxConnectionCount, *params.MaxConnections)
		}
		if params.Threads != nil && (*params.Threads < 1 || *params.Threads > maxThreadCount) {
			return fmt.Errorf("threads must be between 1 and %d, got %d", maxThreadCount, *params.Threads)
		}
	}

	return nil
}
