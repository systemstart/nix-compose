package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func tempDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("opening temp db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestCollectionSaveLoadDelete(t *testing.T) {
	db := tempDB(t)

	type testItem struct {
		Id   string `json:"id"`
		Name string `json:"name"`
	}

	item := &testItem{Id: "item-1", Name: "test"}

	// Save
	err := db.Save(DeploymentsById, &identityWrapper{id: item.Id, data: item})
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load
	raw, err := db.Load(DeploymentsById, "item-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if raw == nil {
		t.Fatal("Load returned nil")
		return
	}

	// Keys
	keys, err := db.Keys(DeploymentsById)
	if err != nil {
		t.Fatalf("Keys failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "item-1" {
		t.Fatalf("unexpected keys: %v", keys)
	}

	// Delete
	err = db.Delete(DeploymentsById, "item-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	raw, err = db.Load(DeploymentsById, "item-1")
	if err != nil {
		t.Fatalf("Load after delete failed: %v", err)
	}
	if raw != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDependencyLinks(t *testing.T) {
	db := tempDB(t)

	vm1 := typing.NewReference("vm1", "resource/v1/virtual-machine")
	vm2 := typing.NewReference("vm2", "resource/v1/virtual-machine")
	network1 := typing.NewReference("network1", "resource/v1/network")

	na1 := typing.NewReference("na1", "arrangement/v1/network-assign")
	na2 := typing.NewReference("na2", "arrangement/v1/network-assign")

	_ = db.AddLink(NewLink(vm1, na1))
	_ = db.AddLink(NewLink(na1, network1))
	_ = db.AddLink(NewLink(vm2, na2))
	_ = db.AddLink(NewLink(na2, network1))

	dependencies, err := db.GetDependencies(vm1)
	if err != nil {
		t.Fatalf("GetDependencies failed: %v", err)
	}
	if len(dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(dependencies))
	}

	depending, err := db.GetDepending(network1)
	if err != nil {
		t.Fatalf("GetDepending failed: %v", err)
	}
	if len(depending) != 2 {
		t.Fatalf("expected 2 depending, got %d", len(depending))
	}
}

func TestRemoveLink(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")
	_ = db.AddLink(NewLink(a, b))

	err := db.RemoveLink(a)
	if err != nil {
		t.Fatalf("RemoveLink failed: %v", err)
	}

	deps, _ := db.GetDependencies(a)
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependencies after remove, got %d", len(deps))
	}

	depending, _ := db.GetDepending(b)
	if len(depending) != 0 {
		t.Fatalf("expected 0 depending after remove, got %d", len(depending))
	}
}

func TestBatch(t *testing.T) {
	db := tempDB(t)

	// Save some items
	for _, id := range []string{"a", "b", "c"} {
		err := db.Save(DeploymentsById, &identityWrapper{id: id, data: map[string]string{"id": id}})
		if err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	var keys []string
	err := db.Batch(DeploymentsById, func(key []byte, _ []byte) {
		keys = append(keys, string(key))
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
}

func TestBatch_EmptyBucket(t *testing.T) {
	db := tempDB(t)

	var count int
	err := db.Batch(DeploymentsById, func(_ []byte, _ []byte) {
		count++
	})
	if err != nil {
		t.Fatalf("Batch on empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
}

func TestBolt_Accessor(t *testing.T) {
	db := tempDB(t)
	if db.Bolt() == nil {
		t.Fatal("Bolt() returned nil")
		return
	}
}

func TestClose_NilBolt(t *testing.T) {
	db := &DB{bolt: nil}
	err := db.Close()
	if err != nil {
		t.Fatalf("expected nil error for nil bolt, got: %v", err)
	}
}

func TestLink_String(t *testing.T) {
	source := typing.NewReference("src-id", "g/v1/Src")
	target := typing.NewReference("tgt-id", "g/v1/Tgt")
	link := NewLink(source, target)
	s := link.String()
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

func TestDefaultDBPath(t *testing.T) {
	path, err := DefaultDBPath()
	if err != nil {
		t.Fatalf("DefaultDBPath: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
}

func TestRemoveLink_MultipleTargets(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")
	c := typing.NewReference("c", "test/v1/C")

	// a depends on b AND c.
	_ = db.AddLink(NewLink(a, b))
	_ = db.AddLink(NewLink(a, c))

	deps, err := db.GetDependencies(a)
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}

	// RemoveLink(a) should remove both.
	if err := db.RemoveLink(a); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}

	deps, _ = db.GetDependencies(a)
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependencies after remove, got %d", len(deps))
	}

	// Reverse links should also be cleaned.
	dependingB, _ := db.GetDepending(b)
	if len(dependingB) != 0 {
		t.Fatalf("expected 0 depending on b, got %d", len(dependingB))
	}
	dependingC, _ := db.GetDepending(c)
	if len(dependingC) != 0 {
		t.Fatalf("expected 0 depending on c, got %d", len(dependingC))
	}
}

func TestRemoveLink_SharedTarget(t *testing.T) {
	db := tempDB(t)

	// Both a and b depend on c.
	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")
	c := typing.NewReference("c", "test/v1/C")

	_ = db.AddLink(NewLink(a, c))
	_ = db.AddLink(NewLink(b, c))

	// c should have 2 depending entries.
	depending, err := db.GetDepending(c)
	if err != nil {
		t.Fatalf("GetDepending: %v", err)
	}
	if len(depending) != 2 {
		t.Fatalf("expected 2 depending, got %d", len(depending))
	}

	// Remove just a's link. c should still have b depending on it.
	if err := db.RemoveLink(a); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}

	depending, err = db.GetDepending(c)
	if err != nil {
		t.Fatalf("GetDepending after remove: %v", err)
	}
	if len(depending) != 1 {
		t.Fatalf("expected 1 depending after removing a, got %d", len(depending))
	}
	if depending[0].GetId() != "b" {
		t.Fatalf("expected b to still depend on c, got %s", depending[0].GetId())
	}
}

func TestRemoveLink_NoLinks(t *testing.T) {
	db := tempDB(t)
	a := typing.NewReference("a", "test/v1/A")

	// RemoveLink on non-existent source should not error.
	if err := db.RemoveLink(a); err != nil {
		t.Fatalf("RemoveLink on empty: %v", err)
	}
}

func TestAddLink_DuplicateIdempotent(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")

	_ = db.AddLink(NewLink(a, b))
	_ = db.AddLink(NewLink(a, b)) // duplicate

	deps, err := db.GetDependencies(a)
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	// Should be unique (deduplicated).
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency (deduplicated), got %d", len(deps))
	}
}

func TestGetDependencies_NoLinks(t *testing.T) {
	db := tempDB(t)
	a := typing.NewReference("a", "test/v1/A")

	deps, err := db.GetDependencies(a)
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependencies, got %d", len(deps))
	}
}

func TestGetDepending_NoLinks(t *testing.T) {
	db := tempDB(t)
	a := typing.NewReference("a", "test/v1/A")

	deps, err := db.GetDepending(a)
	if err != nil {
		t.Fatalf("GetDepending: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 depending, got %d", len(deps))
	}
}

func TestCollectionLoadNotFound(t *testing.T) {
	db := tempDB(t)
	raw, err := db.Load(RolloutsById, "nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil for nonexistent key, got %s", raw)
	}
}

func TestCollectionKeys_Empty(t *testing.T) {
	db := tempDB(t)
	keys, err := db.Keys(RolloutsById)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestCollectionDelete_NonExistent(t *testing.T) {
	db := tempDB(t)
	// Deleting a non-existent key should not error.
	err := db.Delete(DeploymentsById, "nonexistent")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestNewLink_Basic(t *testing.T) {
	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")

	link := NewLink(a, b)
	if link.Source.GetId() != "a" {
		t.Fatalf("Source.GetId() = %q, want a", link.Source.GetId())
	}
	if link.Target.GetId() != "b" {
		t.Fatalf("Target.GetId() = %q, want b", link.Target.GetId())
	}
}

func TestOpen_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bolt")

	// Create and close.
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Re-open existing file.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestDependencyLinks_ChainedDeps(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")
	c := typing.NewReference("c", "test/v1/C")

	// a -> b -> c
	_ = db.AddLink(NewLink(a, b))
	_ = db.AddLink(NewLink(b, c))

	depsA, _ := db.GetDependencies(a)
	if len(depsA) != 1 || depsA[0].GetId() != "b" {
		t.Fatalf("a deps: %v", depsA)
	}

	depsB, _ := db.GetDependencies(b)
	if len(depsB) != 1 || depsB[0].GetId() != "c" {
		t.Fatalf("b deps: %v", depsB)
	}

	dependingC, _ := db.GetDepending(c)
	if len(dependingC) != 1 || dependingC[0].GetId() != "b" {
		t.Fatalf("c depending: %v", dependingC)
	}
}

// identityWrapper adapts a struct to the typing.Identity interface for testing.
type identityWrapper struct {
	id   string
	data any
}

func (w *identityWrapper) GetId() string { return w.id }

func (w *identityWrapper) MarshalJSON() ([]byte, error) {
	return json.Marshal(w.data) //nolint:wrapcheck // test helper, MarshalJSON interface
}

// ===========================================================================
// Additional tests to improve coverage
// ===========================================================================

func TestLink_String_Format(t *testing.T) {
	source := typing.NewReference("src-id", "g/v1/Src")
	target := typing.NewReference("tgt-id", "g/v1/Tgt")
	link := NewLink(source, target)
	s := link.String()
	if !strings.Contains(s, "src-id") {
		t.Fatalf("expected string to contain source id, got %q", s)
	}
	if !strings.Contains(s, "tgt-id") {
		t.Fatalf("expected string to contain target id, got %q", s)
	}
	if !strings.HasPrefix(s, "link[") {
		t.Fatalf("expected string to start with 'link[', got %q", s)
	}
}

func TestNewLink_PanicsOnEmptySourceId(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty source id")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "source has no id") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()

	// SimpleReference with empty Id should trigger the panic.
	emptySource := &typing.SimpleReference{Id: "", Key: "test/v1/A"}
	target := typing.NewReference("tgt", "test/v1/B")
	NewLink(emptySource, target)
}

func TestNewLink_PreservesKeys(t *testing.T) {
	source := typing.NewReference("s1", "group/v1/Source")
	target := typing.NewReference("t1", "group/v1/Target")
	link := NewLink(source, target)

	if link.Source.GetKey() != "group/v1/Source" {
		t.Fatalf("Source.GetKey() = %q, want group/v1/Source", link.Source.GetKey())
	}
	if link.Target.GetKey() != "group/v1/Target" {
		t.Fatalf("Target.GetKey() = %q, want group/v1/Target", link.Target.GetKey())
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	// Try to open a DB in a path that cannot be created (file as directory).
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating blocker file: %v", err)
	}
	// Use the file as if it were a directory.
	path := filepath.Join(blocker, "sub", "test.bolt")
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected error when opening DB in an invalid directory path")
	}
}

func TestOpen_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.bolt")
	// Write garbage data so bbolt cannot open it.
	if err := os.WriteFile(path, []byte("this is not a bolt database"), 0o600); err != nil {
		t.Fatalf("writing corrupt file: %v", err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected error when opening a corrupt bolt file")
	}
}

func TestBatch_WithValues(t *testing.T) {
	db := tempDB(t)

	// Save items and verify Batch receives both keys and values.
	data := map[string]string{"id": "x", "name": "test-item"}
	err := db.Save(DeploymentsById, &identityWrapper{id: "x", data: data})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	var gotKey string
	var gotValue []byte
	err = db.Batch(DeploymentsById, func(key []byte, value []byte) {
		gotKey = string(key)
		gotValue = make([]byte, len(value))
		copy(gotValue, value)
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if gotKey != "x" {
		t.Fatalf("expected key 'x', got %q", gotKey)
	}
	if len(gotValue) == 0 {
		t.Fatal("expected non-empty value")
	}
	// Verify the value can be unmarshaled.
	var parsed map[string]string
	if err := json.Unmarshal(gotValue, &parsed); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}
	if parsed["name"] != "test-item" {
		t.Fatalf("expected name=test-item, got %q", parsed["name"])
	}
}

func TestSaveAndDelete_VerifyCycle(t *testing.T) {
	db := tempDB(t)

	// Save an item.
	err := db.Save(RolloutsById, &identityWrapper{id: "cycle-1", data: map[string]string{"value": "hello"}})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify it exists.
	raw, err := db.Load(RolloutsById, "cycle-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if raw == nil {
		t.Fatal("expected item to exist after Save")
		return
	}

	// Delete it.
	err = db.Delete(RolloutsById, "cycle-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it no longer exists.
	raw, err = db.Load(RolloutsById, "cycle-1")
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if raw != nil {
		t.Fatal("expected item to be gone after Delete")
	}

	// Verify keys list is empty.
	keys, err := db.Keys(RolloutsById)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys after delete, got %d", len(keys))
	}
}

func TestAddLink_MultipleDistinctTargets(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")
	c := typing.NewReference("c", "test/v1/C")
	d := typing.NewReference("d", "test/v1/D")

	// a -> b, a -> c, a -> d
	if err := db.AddLink(NewLink(a, b)); err != nil {
		t.Fatalf("AddLink a->b: %v", err)
	}
	if err := db.AddLink(NewLink(a, c)); err != nil {
		t.Fatalf("AddLink a->c: %v", err)
	}
	if err := db.AddLink(NewLink(a, d)); err != nil {
		t.Fatalf("AddLink a->d: %v", err)
	}

	deps, err := db.GetDependencies(a)
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	// Each target should have 'a' as a dependent.
	for _, target := range []typing.Reference{b, c, d} {
		depending, err := db.GetDepending(target)
		if err != nil {
			t.Fatalf("GetDepending(%s): %v", target.GetId(), err)
		}
		if len(depending) != 1 || depending[0].GetId() != "a" {
			t.Fatalf("expected [a] depending on %s, got %v", target.GetId(), depending)
		}
	}
}

func TestAddLink_DuplicatePreservesReverseLinks(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")

	// Add the same link three times.
	for i := 0; i < 3; i++ {
		if err := db.AddLink(NewLink(a, b)); err != nil {
			t.Fatalf("AddLink iteration %d: %v", i, err)
		}
	}

	// Forward should be deduplicated.
	deps, _ := db.GetDependencies(a)
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(deps))
	}

	// Reverse should also be deduplicated.
	depending, _ := db.GetDepending(b)
	if len(depending) != 1 {
		t.Fatalf("expected 1 depending, got %d", len(depending))
	}
}

func TestSave_MultipleItemsSameCollection(t *testing.T) {
	db := tempDB(t)

	for i := 0; i < 5; i++ {
		id := "item-" + string(rune('A'+i))
		err := db.Save(DeploymentsById, &identityWrapper{
			id:   id,
			data: map[string]string{"id": id, "index": string(rune('0' + i))},
		})
		if err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	keys, err := db.Keys(DeploymentsById)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 5 {
		t.Fatalf("expected 5 keys, got %d", len(keys))
	}
}

func TestSave_OverwriteExistingKey(t *testing.T) {
	db := tempDB(t)

	// Save initial value.
	err := db.Save(DeploymentsById, &identityWrapper{id: "overwrite-1", data: map[string]string{"version": "1"}})
	if err != nil {
		t.Fatalf("Save v1: %v", err)
	}

	// Overwrite with new value.
	err = db.Save(DeploymentsById, &identityWrapper{id: "overwrite-1", data: map[string]string{"version": "2"}})
	if err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	// Load and verify the latest value.
	raw, err := db.Load(DeploymentsById, "overwrite-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["version"] != "2" {
		t.Fatalf("expected version=2, got %q", parsed["version"])
	}

	// Should still be just 1 key.
	keys, err := db.Keys(DeploymentsById)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestRemoveLink_ReAddAfterRemove(t *testing.T) {
	db := tempDB(t)

	a := typing.NewReference("a", "test/v1/A")
	b := typing.NewReference("b", "test/v1/B")

	// Add, remove, then re-add.
	if err := db.AddLink(NewLink(a, b)); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if err := db.RemoveLink(a); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	if err := db.AddLink(NewLink(a, b)); err != nil {
		t.Fatalf("re-AddLink: %v", err)
	}

	deps, _ := db.GetDependencies(a)
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency after re-add, got %d", len(deps))
	}
	depending, _ := db.GetDepending(b)
	if len(depending) != 1 {
		t.Fatalf("expected 1 depending after re-add, got %d", len(depending))
	}
}
