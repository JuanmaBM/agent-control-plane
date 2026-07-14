package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ambient-code/platform/components/ambient-cli/pkg/config"
	"github.com/ambient-code/platform/components/ambient-cli/pkg/connection"
	"github.com/ambient-code/platform/components/ambient-cli/pkg/output"
	sdkclient "github.com/ambient-code/platform/components/ambient-sdk/go-sdk/client"
	sdktypes "github.com/ambient-code/platform/components/ambient-sdk/go-sdk/types"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup <name>",
	Short: "Configure openshell CLI access for a gateway",
	Long: `Configure local openshell CLI access for a named gateway.

Extracts mTLS certificates from the cluster, starts a kubectl port-forward,
and registers the gateway with the openshell CLI.

Requires kubectl and openshell to be installed and a valid kubeconfig context.`,
	Example: "  acpctl gateway setup my-gateway",
	Args:    cobra.ExactArgs(1),
	RunE:    runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	client, err := connection.NewClientFromConfig()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.GetRequestTimeout())
	defer cancel()

	gw, err := findGateway(ctx, client, args[0])
	if err != nil {
		return err
	}

	format, err := output.ParseFormat("")
	if err != nil {
		return err
	}
	printer := output.NewPrinter(format, cmd.OutOrStdout())

	return setupOpenshellGateway(printer.Writer(), gw)
}

func findGateway(ctx context.Context, client *sdkclient.Client, nameOrID string) (*sdktypes.Gateway, error) {
	gw, err := client.Gateways().Get(ctx, nameOrID)
	if err == nil {
		return gw, nil
	}

	page := 1
	pageSize := 100
	for {
		opts := sdktypes.NewListOptions().Page(page).Size(pageSize).Build()
		list, err2 := client.Gateways().List(ctx, opts)
		if err2 != nil {
			return nil, fmt.Errorf("list gateways: %w", err2)
		}
		for i := range list.Items {
			if list.Items[i].Name == nameOrID {
				return &list.Items[i], nil
			}
		}
		if len(list.Items) < pageSize {
			break
		}
		page++
	}
	return nil, fmt.Errorf("gateway %q not found", nameOrID)
}

// TODO: once gateway access via Route/Ingress is supported, use that instead of kubectl port-forward
func setupOpenshellGateway(w io.Writer, gw *sdktypes.Gateway) error {
	namespace := strings.ToLower(gw.ProjectID)
	gwName := gw.Name

	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found in PATH: required for gateway setup")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		return fmt.Errorf("openshell not found in PATH: required for gateway setup")
	}

	if !kubectlNamespaceExists(namespace) {
		return fmt.Errorf("namespace %q does not exist in the cluster", namespace)
	}

	if !kubectlSecretExists(namespace, "openshell-server-tls") {
		return fmt.Errorf("openshell-server-tls secret not found in namespace %q; gateway may not be fully provisioned", namespace)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	certDir := filepath.Join(homeDir, ".config", "openshell", "gateways", gwName, "mtls")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("create cert directory: %w", err)
	}

	fmt.Fprintf(w, "Extracting mTLS certs from openshell-server-tls...\n")
	for _, key := range []string{"ca.crt", "tls.crt", "tls.key"} {
		if err := extractSecretKey(namespace, "openshell-server-tls", key, filepath.Join(certDir, key)); err != nil {
			return fmt.Errorf("extract %s: %w", key, err)
		}
	}

	fmt.Fprintf(w, "Starting port-forward to openshell-gateway in %s...\n", namespace)
	pfCmd := exec.Command("kubectl", "port-forward", "-n", namespace,
		"statefulset/openshell-gateway", ":8080")
	pfCmd.Stderr = os.Stderr
	pfOut, err := pfCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create port-forward pipe: %w", err)
	}
	if err := pfCmd.Start(); err != nil {
		return fmt.Errorf("start port-forward: %w", err)
	}
	defer func() {
		_ = pfCmd.Process.Kill()
		_ = pfCmd.Wait()
	}()

	port := ""
	scanner := bufio.NewScanner(pfOut)
	portCh := make(chan string, 1)
	pfCtx, pfCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pfCancel()
	go func() {
		defer pfCancel()
		for scanner.Scan() {
			line := scanner.Text()
			if idx := strings.Index(line, "Forwarding from 127.0.0.1:"); idx >= 0 {
				rest := line[idx+len("Forwarding from 127.0.0.1:"):]
				if end := strings.Index(rest, " "); end > 0 {
					portCh <- rest[:end]
					return
				}
			}
		}
	}()

	select {
	case port = <-portCh:
	case <-pfCtx.Done():
		return fmt.Errorf("timeout waiting for port-forward to start")
	}

	fmt.Fprintf(w, "Port-forward active on localhost:%s\n", port)

	_ = exec.Command("openshell", "gateway", "remove", gwName).Run()

	fmt.Fprintf(w, "Registering gateway %s -> https://localhost:%s...\n", gwName, port)
	addCmd := exec.Command("openshell", "gateway", "add",
		"--name", gwName, "--local",
		fmt.Sprintf("https://localhost:%s", port))
	addOut, err := addCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openshell gateway add: %s", string(addOut))
	}

	for _, key := range []string{"ca.crt", "tls.crt", "tls.key"} {
		if err := extractSecretKey(namespace, "openshell-server-tls", key, filepath.Join(certDir, key)); err != nil {
			return fmt.Errorf("re-extract %s: %w", key, err)
		}
	}

	fmt.Fprintf(w, "Verifying connectivity...\n")
	statusCmd := exec.Command("openshell", "-g", gwName, "provider", "list")
	if err := statusCmd.Run(); err != nil {
		fmt.Fprintf(w, "Warning: connectivity check failed — verify gateway pod is running:\n")
		fmt.Fprintf(w, "  kubectl logs -l app.kubernetes.io/instance=openshell-gateway -n %s\n", namespace)
	} else {
		fmt.Fprintf(w, "Gateway %s connected successfully\n", gwName)
	}

	fmt.Fprintf(w, "\nUsage:\n")
	fmt.Fprintf(w, "  openshell sandbox list --gateway %s\n", gwName)

	fmt.Fprintf(w, "\nPort-forward is running in the foreground (PID %d). Press Ctrl+C to stop.\n", pfCmd.Process.Pid)

	_ = pfCmd.Wait()

	return nil
}

func kubectlNamespaceExists(namespace string) bool {
	cmd := exec.Command("kubectl", "get", "namespace", namespace)
	return cmd.Run() == nil
}

func kubectlSecretExists(namespace, name string) bool {
	cmd := exec.Command("kubectl", "get", "secret", name, "-n", namespace)
	return cmd.Run() == nil
}

func extractSecretKey(namespace, secretName, key, destPath string) error {
	cmd := exec.Command("kubectl", "get", "secret", secretName,
		"-n", namespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", strings.ReplaceAll(key, ".", "\\.")))
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("kubectl get secret: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		return fmt.Errorf("base64 decode: %w", err)
	}

	perm := os.FileMode(0644)
	if strings.Contains(key, "key") {
		perm = 0600
	}
	return os.WriteFile(destPath, decoded, perm)
}
