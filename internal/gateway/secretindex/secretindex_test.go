package secretindex_test

import (
	"reflect"
	"sort"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	"github.com/kubezap/kubezap-operator/internal/gateway/secretindex"
)

func sortedNames(keys []types.NamespacedName) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	sort.Strings(out)
	return out
}

func TestUpdate_ThenObjectsFor(t *testing.T) {
	idx := secretindex.New()
	obj := types.NamespacedName{Namespace: "ns", Name: "trigger-a"}
	secret := types.NamespacedName{Namespace: "ns", Name: "secret-a"}

	idx.Update(obj, []types.NamespacedName{secret})

	got := idx.ObjectsFor(secret)
	if len(got) != 1 || got[0] != obj {
		t.Fatalf("ObjectsFor: want [%v], got %v", obj, got)
	}
}

func TestObjectsFor_MultipleObjectsSharingOneSecret(t *testing.T) {
	idx := secretindex.New()
	secret := types.NamespacedName{Namespace: "ns", Name: "shared-secret"}
	objA := types.NamespacedName{Namespace: "ns", Name: "trigger-a"}
	objB := types.NamespacedName{Namespace: "ns", Name: "trigger-b"}

	idx.Update(objA, []types.NamespacedName{secret})
	idx.Update(objB, []types.NamespacedName{secret})

	got := sortedNames(idx.ObjectsFor(secret))
	want := sortedNames([]types.NamespacedName{objA, objB})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ObjectsFor: want %v, got %v", want, got)
	}
}

func TestUpdate_ReplacesPreviousSecretSet(t *testing.T) {
	idx := secretindex.New()
	obj := types.NamespacedName{Namespace: "ns", Name: "trigger-a"}
	oldSecret := types.NamespacedName{Namespace: "ns", Name: "old-secret"}
	newSecret := types.NamespacedName{Namespace: "ns", Name: "new-secret"}

	idx.Update(obj, []types.NamespacedName{oldSecret})
	idx.Update(obj, []types.NamespacedName{newSecret}) // e.g. Trigger edited to reference a different secret

	if got := idx.ObjectsFor(oldSecret); got != nil {
		t.Fatalf("ObjectsFor(oldSecret): want nil (stale reference must be dropped), got %v", got)
	}
	got := idx.ObjectsFor(newSecret)
	if len(got) != 1 || got[0] != obj {
		t.Fatalf("ObjectsFor(newSecret): want [%v], got %v", obj, got)
	}
}

func TestUpdate_EmptySecretsRemovesObject(t *testing.T) {
	idx := secretindex.New()
	obj := types.NamespacedName{Namespace: "ns", Name: "trigger-a"}
	secret := types.NamespacedName{Namespace: "ns", Name: "secret-a"}

	idx.Update(obj, []types.NamespacedName{secret})
	idx.Update(obj, nil) // e.g. Trigger edited to remove its auth config entirely

	if got := idx.ObjectsFor(secret); got != nil {
		t.Fatalf("ObjectsFor after Update(obj, nil): want nil, got %v", got)
	}
}

func TestRemove_DropsObjectFromAllSecrets(t *testing.T) {
	idx := secretindex.New()
	obj := types.NamespacedName{Namespace: "ns", Name: "trigger-a"}
	secretA := types.NamespacedName{Namespace: "ns", Name: "secret-a"}
	secretB := types.NamespacedName{Namespace: "ns", Name: "secret-b"}

	idx.Update(obj, []types.NamespacedName{secretA, secretB})
	idx.Remove(obj)

	if got := idx.ObjectsFor(secretA); got != nil {
		t.Fatalf("ObjectsFor(secretA) after Remove: want nil, got %v", got)
	}
	if got := idx.ObjectsFor(secretB); got != nil {
		t.Fatalf("ObjectsFor(secretB) after Remove: want nil, got %v", got)
	}
}

func TestObjectsFor_UnknownSecretReturnsNil(t *testing.T) {
	idx := secretindex.New()
	if got := idx.ObjectsFor(types.NamespacedName{Namespace: "ns", Name: "never-referenced"}); got != nil {
		t.Fatalf("ObjectsFor(unknown secret): want nil, got %v", got)
	}
}
