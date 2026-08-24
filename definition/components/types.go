// Package components contains parameter types for provider component types.
//
// Each struct here corresponds to a component type defined in versions.yaml
// and is converted to an OpenAPI schema during generation.
// Add fields when a component type accepts parameters beyond
// what the base Instance spec provides.
//
// +k8s:openapi-gen=true
package components

// MemcachedParameters are the knobs a memcached component exposes beyond the
// fields every component already has (version, image, replicas, resources,
// storage).
//
// Users set these under `spec.components.engine.parameters` on an Instance;
// the provider reads them back with Context.DecodeComponentParameters.
//
// memcached's memory budget (-m) is not among them: it is derived from the
// component's resource limits, because expressing one quantity twice lets the
// two disagree.
type MemcachedParameters struct {
	// MaxConnections is the maximum number of simultaneous client connections
	// a node accepts (memcached -c). Defaults to the memcached default of 1024.
	// +optional
	MaxConnections *int32 `json:"maxConnections,omitempty"`

	// Threads is the number of worker threads per node (memcached -t).
	// Defaults to the memcached default of 4.
	// +optional
	Threads *int32 `json:"threads,omitempty"`
}
