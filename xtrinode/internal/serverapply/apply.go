package serverapply

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Object applies a typed Kubernetes object using server-side apply.
func Object(ctx context.Context, cli client.Client, scheme *runtime.Scheme, obj client.Object, fieldOwner string, forceOwnership bool) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for %T: %w", obj, err)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	unstructuredObj, err := toUnstructured(obj, gvk)
	if err != nil {
		return err
	}
	return Unstructured(ctx, cli, unstructuredObj, fieldOwner, forceOwnership)
}

// Unstructured applies an unstructured Kubernetes object using server-side apply.
func Unstructured(ctx context.Context, cli client.Client, obj *unstructured.Unstructured, fieldOwner string, forceOwnership bool) error {
	opts := []client.ApplyOption{client.FieldOwner(fieldOwner)}
	if forceOwnership {
		opts = append(opts, client.ForceOwnership)
	}
	return cli.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), opts...)
}

func toUnstructured(obj client.Object, gvk schema.GroupVersionKind) (*unstructured.Unstructured, error) {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert %T to unstructured apply configuration: %w", obj, err)
	}
	unstructuredObj := &unstructured.Unstructured{Object: raw}
	unstructuredObj.SetGroupVersionKind(gvk)
	return unstructuredObj, nil
}
