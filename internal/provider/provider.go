// Package provider implements an OpenEverest provider that runs memcached
// directly on Kubernetes, with no operator in between.
//
// Real providers translate an Instance into a custom resource owned by a
// database operator. This one translates it into a StatefulSet and a headless
// Service, which keeps every OpenEverest concept visible without the reader
// having to learn an operator's API first.
//
// The four methods below are the whole contract. Each delegates to its own
// file so the lifecycle stays readable:
//
//	Validate → validate.go  reject bad specs before they are persisted
//	Sync     → sync.go      make the cluster match the spec
//	Status   → status.go    report what the cluster actually looks like
//	Cleanup  → this file    tear down anything not garbage collected
package provider

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-example/internal/common"
)

// Compile-time check that Provider implements the required interface.
var _ controller.ProviderInterface = (*Provider)(nil)

// Provider implements controller.ProviderInterface for memcached.
type Provider struct {
	controller.BaseProvider
}

// New creates a new Provider.
func New() *Provider {
	return &Provider{
		BaseProvider: controller.BaseProvider{
			ProviderName: common.ProviderName,
			// The runtime's scheme already knows the OpenEverest APIs and
			// core/v1; anything else a provider touches must be registered
			// here, including apps/v1.
			SchemeFuncs: []func(*runtime.Scheme) error{
				appsv1.AddToScheme,
			},
			// Without this watch the Instance would only be reconciled on its
			// own changes, so Status() would never notice pods becoming ready.
			WatchConfigs: []controller.WatchConfig{
				controller.WatchOwned(&appsv1.StatefulSet{}),
			},
		},
	}
}

// Validate rejects Instance specs this provider cannot serve. The API server
// calls it before persisting an Instance; the reconciler calls it again on
// every reconcile, which is what catches specs applied straight to Kubernetes.
func (p *Provider) Validate(c *controller.Context) error {
	return validate(c)
}

// Sync makes the cluster match the Instance spec. It runs on every
// reconciliation and must be idempotent.
func (p *Provider) Sync(c *controller.Context) error {
	return sync(c)
}

// Status reports the observed state of the workload back to the Instance.
func (p *Provider) Status(c *controller.Context) (controller.Status, error) {
	return status(c)
}

// Cleanup runs when an Instance is deleted, before the runtime drops its
// finalizer.
//
// There is nothing to do here: every object this provider creates goes through
// Context.Apply, which sets a controller reference back to the Instance, so
// Kubernetes garbage collects them. Implement this method when a provider
// creates something outside the Instance's ownership graph — a cluster-scoped
// object, or a resource in another namespace — or when teardown has to be
// waited on, in which case return controller.WaitFor to be reconciled again.
func (p *Provider) Cleanup(_ *controller.Context) error {
	return nil
}
