package composition

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

// loadJSONFixture loads a JSON fixture (nix eval output) into a Composition.
func loadJSONFixture(t *testing.T, name string) *eval.Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := eval.ParseComposition(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return comp
}
