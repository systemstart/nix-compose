package cri

import (
	"context"
	"fmt"

	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListImages returns all images known to the CRI runtime.
func (c *Client) ListImages(ctx context.Context) ([]*runtimev1.Image, error) {
	resp, err := c.image.ListImages(ctx, &runtimev1.ListImagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("cri: list images: %w", err)
	}
	return resp.GetImages(), nil
}

// ImageStatus returns the status of a single image, or nil if it is not present.
func (c *Client) ImageStatus(ctx context.Context, image string) (*runtimev1.Image, error) {
	resp, err := c.image.ImageStatus(ctx, &runtimev1.ImageStatusRequest{
		Image: &runtimev1.ImageSpec{Image: image},
	})
	if err != nil {
		return nil, fmt.Errorf("cri: image status %s: %w", image, err)
	}
	return resp.GetImage(), nil
}

// RemoveImage removes an image from the CRI runtime.
// It is idempotent per the CRI spec — removing a non-existent image is not an error.
func (c *Client) RemoveImage(ctx context.Context, image string) error {
	_, err := c.image.RemoveImage(ctx, &runtimev1.RemoveImageRequest{
		Image: &runtimev1.ImageSpec{Image: image},
	})
	if err != nil {
		return fmt.Errorf("cri: remove image %s: %w", image, err)
	}
	return nil
}
