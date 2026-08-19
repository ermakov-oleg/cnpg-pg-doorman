package operator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ermakov-oleg/cnpg-pg-doorman/pkg/metadata"
)

type jsonPatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func mutateCluster(t *testing.T, cluster *cnpgv1.Cluster) []jsonPatchOp {
	t.Helper()

	definition, err := json.Marshal(cluster)
	if err != nil {
		t.Fatal(err)
	}

	result, err := Implementation{}.MutateCluster(context.Background(), &operator.OperatorMutateClusterRequest{
		Definition: definition,
	})
	if err != nil {
		t.Fatalf("MutateCluster: %v", err)
	}

	var ops []jsonPatchOp
	if len(result.JsonPatch) > 0 {
		if err := json.Unmarshal(result.JsonPatch, &ops); err != nil {
			t.Fatalf("unmarshaling patch %q: %v", result.JsonPatch, err)
		}
	}
	return ops
}

func mutationTestCluster(plugins ...cnpgv1.PluginConfiguration) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "postgresql.cnpg.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "pg-test", Namespace: "ns"},
		Spec:       cnpgv1.ClusterSpec{Plugins: plugins},
	}
}

// pluginParamOps returns the patch ops targeting our plugin's parameters,
// serialized for content assertions.
func pluginParamOps(t *testing.T, ops []jsonPatchOp) string {
	t.Helper()
	var b strings.Builder
	for _, op := range ops {
		if !strings.HasPrefix(op.Path, "/spec/plugins/") {
			continue
		}
		b.WriteString(op.Path)
		b.WriteString("=")
		b.Write(op.Value)
		b.WriteString(";")
	}
	return b.String()
}

func TestMutateClusterAppliesDefaultPorts(t *testing.T) {
	cluster := mutationTestCluster(cnpgv1.PluginConfiguration{
		Name:       metadata.PluginName,
		Parameters: map[string]string{"configName": "doorman-cfg"},
	})

	ops := mutateCluster(t, cluster)
	if len(ops) == 0 {
		t.Fatal("expected a non-empty patch adding default ports")
	}

	patched := pluginParamOps(t, ops)
	if !strings.Contains(patched, "6432") {
		t.Errorf("patch does not set default poolerPort 6432: %s", patched)
	}
	if !strings.Contains(patched, "9127") {
		t.Errorf("patch does not set default metricsPort 9127: %s", patched)
	}
}

func TestMutateClusterKeepsExplicitPoolerPort(t *testing.T) {
	cluster := mutationTestCluster(cnpgv1.PluginConfiguration{
		Name: metadata.PluginName,
		Parameters: map[string]string{
			"configName": "doorman-cfg",
			"poolerPort": "7000",
		},
	})

	ops := mutateCluster(t, cluster)
	patched := pluginParamOps(t, ops)

	if strings.Contains(patched, "poolerPort") {
		t.Errorf("explicit poolerPort must not be overridden: %s", patched)
	}
	if !strings.Contains(patched, "9127") {
		t.Errorf("missing metricsPort default must still be applied: %s", patched)
	}
}

func TestMutateClusterWithoutPluginIsUntouched(t *testing.T) {
	ops := mutateCluster(t, mutationTestCluster())
	if len(ops) != 0 {
		t.Errorf("cluster without plugins must produce an empty patch, got %+v", ops)
	}
}

func TestMutateClusterIgnoresForeignPlugin(t *testing.T) {
	cluster := mutationTestCluster(cnpgv1.PluginConfiguration{
		Name:       "other-plugin.example.io",
		Parameters: map[string]string{"foo": "bar"},
	})

	ops := mutateCluster(t, cluster)
	if len(ops) != 0 {
		t.Errorf("foreign plugin must not be mutated, got %+v", ops)
	}
}
