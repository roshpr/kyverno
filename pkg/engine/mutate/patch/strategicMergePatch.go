package patch

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/kustomize/api/filters/patchstrategicmerge"
	filtersutil "sigs.k8s.io/kustomize/kyaml/filtersutil"
	yaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

// ProcessStrategicMergePatch ...
func ProcessStrategicMergePatch(logger logr.Logger, overlay interface{}, resource resource) (resource, error) {
	overlayBytes, err := json.Marshal(overlay)
	if err != nil {
		logger.Error(err, "failed to marshal resource")
		return nil, err
	}
	patchedBytes, err := strategicMergePatch(logger, string(resource), string(overlayBytes))
	if err != nil {
		logger.Error(err, "failed to apply patchStrategicMerge")
		return nil, err
	}
	return patchedBytes, nil
}

func strategicMergePatch(logger logr.Logger, base, overlay string) ([]byte, error) {
	// Escape before both parses. preProcessStrategicMergePatch reads the
	// documents, and filtersutil.ApplyToJSON parses base again.
	base = escapeYAMLIncompatibleJSON(base)
	overlay = escapeYAMLIncompatibleJSON(overlay)

	preprocessedYaml, err := preProcessStrategicMergePatch(logger, overlay, base)
	if err != nil {
		_, isConditionError := err.(ConditionError)
		_, isGlobalConditionError := err.(GlobalConditionError)

		if isConditionError || isGlobalConditionError {
			if err = preprocessedYaml.UnmarshalJSON([]byte(`{}`)); err != nil {
				return []byte{}, err
			}
		} else {
			return []byte{}, fmt.Errorf("failed to preProcess rule: %+v", err)
		}
	}

	patchStr, _ := preprocessedYaml.String()
	logger.V(3).Info("applying strategic merge patch", "patch", patchStr)
	f := patchstrategicmerge.Filter{
		Patch: preprocessedYaml,
	}

	baseObj := buffer{Buffer: bytes.NewBufferString(base)}
	err = filtersutil.ApplyToJSON(f, baseObj)

	return baseObj.Bytes(), err
}

func preProcessStrategicMergePatch(logger logr.Logger, pattern, resource string) (*yaml.RNode, error) {
	patternNode, err := yaml.Parse(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to parse strategic merge overlay: %w", err)
	}
	resourceNode, err := yaml.Parse(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}

	err = PreProcessPattern(logger, patternNode, resourceNode)
	return patternNode, err
}
