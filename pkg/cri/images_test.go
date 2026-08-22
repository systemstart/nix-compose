package cri

import (
	"context"
	"testing"
)

func TestListImages(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Pull two images.
	if err := c.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if err := c.PullImage(ctx, "redis:7"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	imgs, err := c.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("expected 2 images, got %d", len(imgs))
	}

	found := map[string]bool{}
	for _, img := range imgs {
		if len(img.RepoTags) > 0 {
			found[img.RepoTags[0]] = true
		}
	}
	if !found["nginx:latest"] {
		t.Error("expected nginx:latest in list")
	}
	if !found["redis:7"] {
		t.Error("expected redis:7 in list")
	}
}

func TestListImagesEmpty(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	imgs, err := c.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 0 {
		t.Fatalf("expected 0 images, got %d", len(imgs))
	}
}

func TestImageStatus_Found(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	img, err := c.ImageStatus(ctx, "nginx:latest")
	if err != nil {
		t.Fatalf("ImageStatus: %v", err)
	}
	if img == nil {
		t.Fatal("expected non-nil image")
		return
	}
	if img.Id != "sha256:nginx:latest" {
		t.Errorf("image ID = %q, want sha256:nginx:latest", img.Id)
	}
}

func TestImageStatus_NotFound(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	img, err := c.ImageStatus(ctx, "nonexistent:image")
	if err != nil {
		t.Fatalf("ImageStatus: %v", err)
	}
	if img != nil {
		t.Fatalf("expected nil image for unknown image, got %v", img)
	}
}

func TestRemoveImage(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	// Verify image exists.
	mock.mu.Lock()
	if _, ok := mock.images["nginx:latest"]; !ok {
		t.Fatal("image should exist after pull")
	}
	mock.mu.Unlock()

	if err := c.RemoveImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("RemoveImage: %v", err)
	}

	// Verify image is gone.
	mock.mu.Lock()
	if _, ok := mock.images["nginx:latest"]; ok {
		t.Error("image should have been removed")
	}
	mock.mu.Unlock()

	// Also verify via ListImages.
	imgs, err := c.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected 0 images after removal, got %d", len(imgs))
	}
}

func TestRemoveImage_Idempotent(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Remove a non-existent image — should not error.
	if err := c.RemoveImage(ctx, "nonexistent:image"); err != nil {
		t.Fatalf("RemoveImage of non-existent image should not error: %v", err)
	}
}
