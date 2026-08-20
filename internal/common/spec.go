// Package common defines constants shared between the provider implementation
// and the definition files under definition/.
//
// Component and topology names are plain strings on both sides of that
// boundary, so they are spelled out once here instead of at every use.
package common

const (
	// ProviderName must match `name` in definition/provider.yaml. The runtime
	// uses it to fetch this provider's own Provider CR, which is where the
	// version catalog and the parameter schemas come from.
	ProviderName = "example"

	// ComponentEngine is the only component this provider defines.
	ComponentEngine = "engine"

	// ComponentTypeMemcached is the software the engine component runs.
	ComponentTypeMemcached = "memcached"

	// TopologyPool is the only architecture this provider offers: independent
	// nodes that clients shard keys across.
	TopologyPool = "pool"

	// MemcachedPort is the port memcached listens on.
	MemcachedPort = 11211
)
