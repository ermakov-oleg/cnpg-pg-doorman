package command

import (
	"bytes"
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type ContainerLocator struct {
	NamespaceName string
	PodName       string
	ContainerName string
}

func ExecuteInContainer(
	ctx context.Context,
	clientSet *kubernetes.Clientset,
	cfg *rest.Config,
	locator ContainerLocator,
	stdin *bytes.Buffer,
	command []string,
) (string, string, error) {
	req := clientSet.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(locator.PodName).
		Namespace(locator.NamespaceName).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: locator.ContainerName,
		Command:   command,
		Stdin:     stdin != nil,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewWebSocketExecutor(cfg, "GET", req.URL().String())
	if err != nil {
		return "", "", err
	}

	var stdinReader io.Reader
	if stdin != nil {
		stdinReader = stdin
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdinReader,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	return stdout.String(), stderr.String(), err
}
