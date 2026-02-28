package namespace

import (
	"context"
	"fmt"
	"math/rand"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateUniqueNamespace(ctx context.Context, cl client.Client, prefix string) (*corev1.Namespace, error) {
	name := fmt.Sprintf("%s-%d", prefix, rand.Intn(100000))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := cl.Create(ctx, ns); err != nil {
		return nil, err
	}
	return ns, nil
}
