package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NamespaceConfig represents a single namespace entry from platform-config
type NamespaceConfig struct {
	Name    string        `yaml:"name"`
	Gateway GatewayConfig `yaml:"gateway"`
}

// GatewayConfig contains gateway-specific configuration for a namespace
type GatewayConfig struct {
	Image          string   `yaml:"image"`
	ServerDnsNames []string `yaml:"serverDnsNames"`
	Config         string   `yaml:"config"` // TOML content
}

// LoadPlatformConfig reads platform-config ConfigMap from ACP namespace
func LoadPlatformConfig(ctx context.Context, clientset *kubernetes.Clientset, namespace string) ([]NamespaceConfig, error) {
	cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, "platform-config", metav1.GetOptions{})
	if err != nil {
		log.Error().
			Str("configmap", "platform-config").
			Str("namespace", namespace).
			Err(err).
			Msg("failed to load platform-config ConfigMap")
		return []NamespaceConfig{}, fmt.Errorf("load platform-config: %w", err)
	}

	namespacesYAML, ok := cm.Data["namespaces"]
	if !ok {
		log.Error().
			Str("configmap", "platform-config").
			Msg("platform-config missing 'namespaces' key")
		return []NamespaceConfig{}, fmt.Errorf("platform-config missing 'namespaces' key")
	}

	var namespaces []NamespaceConfig
	if err := yaml.Unmarshal([]byte(namespacesYAML), &namespaces); err != nil {
		log.Error().
			Str("configmap", "platform-config").
			Err(err).
			Msg("failed to parse platform-config namespaces YAML")
		return []NamespaceConfig{}, fmt.Errorf("parse platform-config: %w", err)
	}

	log.Info().
		Str("configmap", "platform-config").
		Int("namespace_count", len(namespaces)).
		Msg("loaded platform-config")

	return namespaces, nil
}

// WatchPlatformConfig sets up a periodic poll (30s) to detect ConfigMap changes
func WatchPlatformConfig(ctx context.Context, clientset *kubernetes.Clientset, namespace string, onChange func([]NamespaceConfig)) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Info().
		Str("configmap", "platform-config").
		Msg("starting platform-config watcher (30s poll)")

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("platform-config watcher stopped")
			return ctx.Err()
		case <-ticker.C:
			configs, err := LoadPlatformConfig(ctx, clientset, namespace)
			if err != nil {
				log.Warn().
					Err(err).
					Msg("platform-config reload failed, will retry")
				continue
			}
			onChange(configs)
		}
	}
}
