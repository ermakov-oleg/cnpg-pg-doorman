package wrapper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// BinarySpecKey is the rendered Secret key describing the desired
// pg_doorman binary (delivery endpoint, per-arch digests, CA bundle).
const BinarySpecKey = "binary.json"

// BinarySpec is the contract between the plugin controller and the wrapper:
// the wrapper trusts it because it arrives via the controller-owned Secret.
type BinarySpec struct {
	URL      string            `json:"url"`
	SHA256   map[string]string `json:"sha256"`
	CABundle string            `json:"caBundle,omitempty"`
}

func ParseBinarySpec(data []byte) (*BinarySpec, error) {
	var spec BinarySpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("invalid binary spec: %w", err)
	}
	if spec.URL == "" {
		return nil, fmt.Errorf("binary spec: url is required")
	}
	if len(spec.SHA256) == 0 {
		return nil, fmt.Errorf("binary spec: sha256 map is required")
	}
	return &spec, nil
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
