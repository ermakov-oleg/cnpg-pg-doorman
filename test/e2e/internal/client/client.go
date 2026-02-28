package client

import (
	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pgdoormanv1alpha1 "github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

func NewClient() (client.Client, *kubernetes.Clientset, *rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, nil, nil, err
	}

	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = cnpgv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = pgdoormanv1alpha1.AddToScheme(scheme)

	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, nil, err
	}

	return cl, clientset, config, nil
}
