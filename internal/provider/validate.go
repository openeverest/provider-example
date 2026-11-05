package provider

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"

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

// validate checks what only this provider knows and reports every error on the
// field at fault, so the message reads well in a form and in `kubectl describe`.
//
// It reads nothing but its input. By the time it runs, the runtime has checked
// the Instance against what the Provider CR declares: version bundles,
// topologies, component names, types and versions, and the parameter schema.
func validate(in controller.ValidationInput) controller.ValidationResult {
	enginePath := field.NewPath("spec", "components").Key(common.ComponentEngine)
	engine, ok := in.Instance.Spec.Components[common.ComponentEngine]
	if !ok {
		// The runtime requires the topology's components only when spec.topology.type is set.
		return controller.ValidationResult{Errors: field.ErrorList{field.Required(enginePath, "")}}
	}

	errs := outOfRange(enginePath.Child("replicas"), engine.Replicas, maxPoolNodes)

	if engine.Parameters != nil && engine.Parameters.Raw != nil {
		parametersPath := enginePath.Child("parameters")
		var params components.MemcachedParameters
		if err := json.Unmarshal(engine.Parameters.Raw, &params); err != nil {
			errs = append(errs, field.Invalid(parametersPath, field.OmitValueType{}, err.Error()))
		} else {
			errs = append(errs, outOfRange(parametersPath.Child("maxConnections"), params.MaxConnections, maxConnectionCount)...)
			errs = append(errs, outOfRange(parametersPath.Child("threads"), params.Threads, maxThreadCount)...)
		}
	}

	return controller.ValidationResult{Errors: errs}
}

// outOfRange reports value unless it is unset or between 1 and limit.
func outOfRange(path *field.Path, value *int32, limit int32) field.ErrorList {
	if value == nil || (*value >= 1 && *value <= limit) {
		return nil
	}
	return field.ErrorList{field.Invalid(path, *value, fmt.Sprintf("must be between 1 and %d", limit))}
}
