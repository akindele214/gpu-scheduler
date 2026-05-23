package inference_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebalanceScriptScalePrefillUpUsesAddPrefillManifest(t *testing.T) {
	log := runRebalanceScript(t, "scale-prefill-up")

	requireLogContains(t, log, "apply -f")
	requireLogContains(t, log, "inference-disagg-rebalance-add-prefill.yaml")
	requireLogContains(t, log, "-n script-test wait --for=condition=Ready pod/inference-prefill-1 --timeout=15m")
}

func TestRebalanceScriptScaleDecodeUpUsesAddDecodeManifest(t *testing.T) {
	log := runRebalanceScript(t, "scale-decode-up")

	requireLogContains(t, log, "apply -f")
	requireLogContains(t, log, "inference-disagg-rebalance-add-decode.yaml")
	requireLogContains(t, log, "-n script-test wait --for=condition=Ready pod/inference-decode-1 --timeout=15m")
}

func TestRebalanceScriptApplyBaseWaitsForBasePods(t *testing.T) {
	log := runRebalanceScript(t, "apply-base")

	requireLogContains(t, log, "apply -f")
	requireLogContains(t, log, "inference-disagg-rebalance-base.yaml")
	requireLogContains(t, log, "-n script-test wait --for=condition=Ready pod/inference-prefill-0 --timeout=15m")
	requireLogContains(t, log, "-n script-test wait --for=condition=Ready pod/inference-decode-0 --timeout=15m")
}

func TestRebalanceScriptCreatesHFTokenSecretWhenConfigured(t *testing.T) {
	log := runRebalanceScript(t, "scale-decode-up", "HF_TOKEN=test-token")

	requireLogContains(t, log, "-n script-test create secret generic hf-token")
	requireLogContains(t, log, "--from-literal=HF_TOKEN=test-token")
	requireLogContains(t, log, "apply -f -")
	requireLogContains(t, log, "inference-disagg-rebalance-add-decode.yaml")
}

func TestRebalanceScriptFailsWhenKubectlFails(t *testing.T) {
	_, err := runRebalanceScriptCommand(t, "scale-decode-up", "FAKE_KUBECTL_FAIL=true")
	if err == nil {
		t.Fatal("expected script to fail when fake kubectl fails")
	}
}

func runRebalanceScript(t *testing.T, action string, extraEnv ...string) string {
	t.Helper()

	log, err := runRebalanceScriptCommand(t, action, extraEnv...)
	if err != nil {
		t.Fatalf("expected script action %q to succeed: %v", action, err)
	}
	return log
}

func runRebalanceScriptCommand(t *testing.T, action string, extraEnv ...string) (string, error) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	fakeKubectlBin := filepath.Join(repoRoot, "fake-kubectl-bin")
	logPath := filepath.Join(t.TempDir(), "fake-kubectl.log")

	cmd := exec.Command("bash", "./inference-disagg-rebalance.sh", action)
	cmd.Env = append(os.Environ(),
		"PATH="+fakeKubectlBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_KUBECTL_LOG="+logPath,
		"NAMESPACE=script-test",
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	output, runErr := cmd.CombinedOutput()
	logBytes, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read fake kubectl log: %v", readErr)
	}
	if runErr != nil {
		return string(logBytes) + "\nscript output:\n" + string(output), runErr
	}

	return string(logBytes), nil
}

func requireLogContains(t *testing.T, log string, want string) {
	t.Helper()

	if !strings.Contains(log, want) {
		t.Fatalf("expected fake kubectl log to contain %q\nlog:\n%s", want, log)
	}
}
