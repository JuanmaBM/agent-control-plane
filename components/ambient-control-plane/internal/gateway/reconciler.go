package gateway

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// ReconcileGateways ensures gateways are deployed in all configured namespaces
func ReconcileGateways(
	ctx context.Context,
	dynamicClient dynamic.Interface,
	clientset *kubernetes.Clientset,
	namespaceConfigs []NamespaceConfig,
	manifests map[string][]*unstructured.Unstructured,
	platformConfigCM *v1.ConfigMap,
) error {
	defaultImage := os.Getenv("OPENSHELL_GATEWAY_IMAGE")
	if defaultImage == "" {
		defaultImage = "ghcr.io/nvidia/openshell/gateway:0.0.71" // Fallback
	}

	for _, nsConfig := range namespaceConfigs {
		// 1. Validate namespace exists
		if !namespaceExists(ctx, clientset, nsConfig.Name) {
			log.Warn().
				Str("namespace", nsConfig.Name).
				Msg("namespace not found in cluster, skipping gateway deployment")
			continue
		}

		// 2. Validate gateway configuration
		if err := ValidateGatewayConfig(nsConfig.Gateway); err != nil {
			log.Error().
				Str("namespace", nsConfig.Name).
				Err(err).
				Msg("invalid gateway configuration")
			continue
		}

		// 3. Deploy/update gateway manifests (reconcile pattern)
		if err := deployGateway(ctx, dynamicClient, nsConfig, manifests, defaultImage, platformConfigCM); err != nil {
			log.Error().
				Str("namespace", nsConfig.Name).
				Err(err).
				Msg("failed to deploy gateway")
			continue // Don't block other namespaces
		}

		log.Info().
			Str("namespace", nsConfig.Name).
			Str("image", defaultImage).
			Msg("gateway reconciled successfully")
	}

	return nil
}

// namespaceExists checks if a namespace exists in the cluster
func namespaceExists(ctx context.Context, clientset *kubernetes.Clientset, namespace string) bool {
	_, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	return err == nil
}

// deployGateway applies all gateway manifests to the namespace using update-or-create pattern
func deployGateway(
	ctx context.Context,
	dynamicClient dynamic.Interface,
	nsConfig NamespaceConfig,
	manifests map[string][]*unstructured.Unstructured,
	defaultImage string,
	platformConfigCM *v1.ConfigMap,
) error {
	// Apply manifests in order: RBAC → ServiceAccount → ConfigMap → Job → Service → StatefulSet → NetworkPolicy
	order := []string{
		"rbac.yaml",
		"serviceaccount.yaml",
		"configmap.yaml",
		"certgen-job.yaml",
		"service.yaml",
		"statefulset.yaml",
		"networkpolicy.yaml",
	}

	for _, filename := range order {
		resources, ok := manifests[filename]
		if !ok {
			log.Warn().
				Str("file", filename).
				Msg("manifest file not found, skipping")
			continue
		}

		for _, manifest := range resources {
			// Apply namespace and image substitutions
			obj, err := ApplyManifestToNamespace(manifest, nsConfig.Name, nsConfig.Gateway, defaultImage)
			if err != nil {
				return fmt.Errorf("apply substitutions for %s: %w", filename, err)
			}

			// Apply config overrides (serverDnsNames, custom TOML)
			if err := ApplyConfigOverrides(obj, nsConfig.Gateway); err != nil {
				return fmt.Errorf("apply config overrides for %s: %w", filename, err)
			}

			// Set OwnerReference to platform-config ConfigMap for garbage collection
			if platformConfigCM != nil {
				setOwnerReference(obj, platformConfigCM)
			}

			// Reconcile resource (update-or-create)
			if err := reconcileResource(ctx, dynamicClient, obj); err != nil {
				return fmt.Errorf("reconcile resource from %s: %w", filename, err)
			}

			log.Debug().
				Str("namespace", nsConfig.Name).
				Str("kind", obj.GetKind()).
				Str("name", obj.GetName()).
				Msg("reconciled gateway resource")
		}
	}

	return nil
}

// reconcileResource creates or updates a Kubernetes resource
func reconcileResource(ctx context.Context, dynamicClient dynamic.Interface, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: kindToResource(gvk.Kind),
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()

	// Determine if resource is namespace-scoped or cluster-scoped
	var resourceClient dynamic.ResourceInterface
	if namespace != "" {
		resourceClient = dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = dynamicClient.Resource(gvr)
	}

	// Try to get existing resource
	existing, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Resource doesn't exist, create it
			_, err = resourceClient.Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("create resource: %w", err)
			}
			log.Debug().
				Str("kind", gvk.Kind).
				Str("name", name).
				Str("namespace", namespace).
				Msg("created new resource")
			return nil
		}
		return fmt.Errorf("get resource: %w", err)
	}

	// Resource exists, update it
	// Preserve resourceVersion for optimistic concurrency
	obj.SetResourceVersion(existing.GetResourceVersion())

	_, err = resourceClient.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update resource: %w", err)
	}

	log.Debug().
		Str("kind", gvk.Kind).
		Str("name", name).
		Str("namespace", namespace).
		Msg("updated existing resource")

	return nil
}

// kindToResource converts Kind to resource name using explicit allowlist
func kindToResource(kind string) string {
	// Explicit mapping for all resource types used in gateway manifests
	mapping := map[string]string{
		"ServiceAccount":     "serviceaccounts",
		"ConfigMap":          "configmaps",
		"Service":            "services",
		"StatefulSet":        "statefulsets",
		"Deployment":         "deployments",
		"Job":                "jobs",
		"Role":               "roles",
		"RoleBinding":        "rolebindings",
		"ClusterRole":        "clusterroles",
		"ClusterRoleBinding": "clusterrolebindings",
		"NetworkPolicy":      "networkpolicies",
		"Secret":             "secrets",
	}

	if resource, ok := mapping[kind]; ok {
		return resource
	}

	// Fallback for unknown types (logged as debug)
	log.Debug().Str("kind", kind).Msg("unknown kind, using naive plural")
	return strings.ToLower(kind) + "s"
}

// setOwnerReference sets the platform-config ConfigMap as owner of the resource
// for automatic garbage collection when the ConfigMap is deleted
func setOwnerReference(obj *unstructured.Unstructured, platformConfigCM *v1.ConfigMap) {
	controller := true
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       platformConfigCM.Name,
			UID:        platformConfigCM.UID,
			Controller: &controller,
			// BlockOwnerDeletion omitted per project convention
		},
	})
}
