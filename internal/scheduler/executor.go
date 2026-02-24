package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// PodExecutor abstracts K8s pod operations for testability.
type PodExecutor interface {
	ExecInPod(ctx context.Context, namespace, podName, container string, cmd []string) error
	DeletePod(ctx context.Context, namespace, podName string, gracePeriodSeconds int64) error
}

type K8sExecutor struct {
	clientSet  kubernetes.Interface
	restConfig *rest.Config
}

func NewK8sExecutor(clientSet kubernetes.Interface, restConfig *rest.Config) *K8sExecutor {
	return &K8sExecutor{clientSet: clientSet, restConfig: restConfig}
}

func (e *K8sExecutor) ExecInPod(ctx context.Context, namespace, podName, container string, cmd []string) error {
	if e.restConfig == nil {
		log.Printf("[EXECUTOR] No REST config available, skipping exec in %s/%s: %v", namespace, podName, cmd)
		return nil
	}

	// If no container specified, pick the first one
	if container == "" {
		pod, err := e.clientSet.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pod: %w", err)
		}
		if len(pod.Spec.Containers) == 0 {
			return fmt.Errorf("pod %s/%s has no containers", namespace, podName)
		}
		container = pod.Spec.Containers[0].Name
	}

	req := e.clientSet.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("exec failed (stderr: %s): %w", stderr.String(), err)
	}

	log.Printf("[EXECUTOR] Exec in %s/%s completed: %s", namespace, podName, stdout.String())
	return nil
}

func (e *K8sExecutor) DeletePod(ctx context.Context, namespace, podName string, gracePeriodSeconds int64) error {
	return e.clientSet.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	})
}
