package testsock

import (
	"net"
	"strings"
	"testing"
)

func TestPath_IsBindable(t *testing.T) {
	p := Path(t, "cri.sock")
	lis, err := net.Listen("unix", p)
	if err != nil {
		t.Fatalf("binding %q (%d bytes): %v", p, len(p), err)
	}
	_ = lis.Close()
}

// TestPath_LongTestNameStillBindable is the regression: the failure only
// appeared for tests whose names were long enough to push the generated
// t.TempDir() path over the limit.
func TestPath_LongTestNameStillBindable(t *testing.T) {
	t.Run(strings.Repeat("VeryLongSubtestName", 6), func(t *testing.T) {
		p := Path(t, "cri.sock")
		if len(p) > maxLen {
			t.Fatalf("path %q is %d bytes, over the %d-byte limit", p, len(p), maxLen)
		}
		lis, err := net.Listen("unix", p)
		if err != nil {
			t.Fatalf("binding %q (%d bytes): %v", p, len(p), err)
		}
		_ = lis.Close()
	})
}
