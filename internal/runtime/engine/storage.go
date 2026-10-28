package engine

import (
	"context"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// StorageClient is optional for monitoring, mandatory for retention evidence.
type StorageClient interface {
	VolumeList(context.Context) ([]volume.Volume, error)
	ImageList(context.Context) ([]image.Summary, error)
	ImageInspect(context.Context, string) (image.InspectResponse, error)
}

func (c *sdkClient) VolumeList(ctx context.Context) ([]volume.Volume, error) {
	result, err := c.value.VolumeList(ctx, client.VolumeListOptions{})
	return result.Items, err
}
func (c *sdkClient) ImageInspect(ctx context.Context, ref string) (image.InspectResponse, error) {
	result, err := c.value.ImageInspect(ctx, ref)
	return result.InspectResponse, err
}

func (c *sdkClient) ImageList(ctx context.Context) ([]image.Summary, error) {
	result, err := c.value.ImageList(ctx, client.ImageListOptions{All: true})
	return result.Items, err
}
