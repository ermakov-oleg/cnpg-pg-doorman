package utils

import (
	"encoding/json"
	"fmt"
)

func GetKind(definition []byte) (string, error) {
	var meta struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(definition, &meta); err != nil {
		return "", fmt.Errorf("failed to unmarshal object definition: %w", err)
	}
	return meta.Kind, nil
}
