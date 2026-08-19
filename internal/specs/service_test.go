package specs

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildDoormanService(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "ns"},
	}
	svc := BuildDoormanService(cluster, 6432)

	if svc.Name != "my-cluster-doorman-rw" {
		t.Errorf("service name = %q", svc.Name)
	}
	if svc.Spec.Selector["cnpg.io/instanceRole"] != "primary" {
		t.Errorf("selector must target the primary, got %v", svc.Spec.Selector)
	}
	if svc.Spec.Selector["cnpg.io/cluster"] != "my-cluster" {
		t.Errorf("selector must scope to the cluster, got %v", svc.Spec.Selector)
	}
	if got := svc.Spec.Ports[0].TargetPort.IntValue(); got != 6432 {
		t.Errorf("targetPort = %d, want pooler port 6432", got)
	}
	if got := svc.Spec.Ports[0].Port; got != 5432 {
		t.Errorf("port = %d, want 5432 (drop-in for the CNPG -rw service)", got)
	}
}
