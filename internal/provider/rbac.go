package provider

// Run `make manifests` to regenerate config/rbac/role.yaml from these markers.
// This file contains kubebuilder RBAC markers for controller-gen.
// See: https://book.kubebuilder.io/reference/markers/rbac

// Base RBAC (required by all providers):
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.openeverest.io,resources=providers,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// The startup gate. Before it starts the controllers, provider-runtime checks
// that the installed OpenEverest supports the version this build is stamped
// with. It reads the versions OpenEverest supports on the Provider CRD and,
// until they include this build's, keeps the pod running but not ready, with a
// Warning Event on the Provider. Once the build starts, it records its version
// on its Provider's status, so a restart of the same build is not checked
// again. controller-gen does not read the markers of a dependency, so every
// provider grants these itself; without them a new build waits with a
// "forbidden" Event. Keep resourceNames in sync with common.ProviderName.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=providers.core.openeverest.io,verbs=get
// +kubebuilder:rbac:groups=core.openeverest.io,resources=providers/status,resourceNames=example,verbs=patch

// =============================================================================
// PROVIDER-SPECIFIC RBAC
// =============================================================================
// A provider backed by an operator would grant itself access to that
// operator's CRs here. This one manages the workload directly, so it needs the
// built-in resources it creates, plus read access to the pods behind them.

// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Secrets are not optional, even though this provider never touches one: the
// runtime writes the connection details returned by Status() into a Secret
// through the manager's cached client. Without this the Secret informer never
// syncs, and every reconcile that reports connection details fails before it
// can write the Instance's status.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
