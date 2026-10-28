package projectlife

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func localNodeReader(cli *client.Client) NodeReader {
	return func(ctx context.Context, node string) (NodeStorage, error) {
		info, err := cli.Info(ctx, client.InfoOptions{})
		if err != nil || info.Info.Swarm.NodeID != node {
			return NodeStorage{}, ErrDataRetention
		}
		result, err := cli.VolumeList(ctx, client.VolumeListOptions{})
		if err != nil {
			return NodeStorage{}, err
		}
		o := NodeStorage{NodeID: node, At: time.Now().UTC(), Complete: true, Volumes: map[string]time.Time{}, VolumeOwners: map[string]string{}, Images: map[string][]string{}, Containers: map[string]container.InspectResponse{}}
		for _, v := range result.Items {
			if v.Driver != "local" {
				continue
			}
			created, e := time.Parse(time.RFC3339Nano, v.CreatedAt)
			if e != nil {
				return o, ErrDataRetention
			}
			o.Volumes[v.Name] = created
			o.VolumeOwners[v.Name] = v.Labels["com.docker.stack.namespace"]
		}
		images, err := cli.ImageList(ctx, client.ImageListOptions{All: true})
		if err != nil {
			return o, err
		}
		for _, image := range images.Items {
			metadata, err := cli.ImageInspect(ctx, image.ID)
			if err != nil || metadata.Config == nil {
				return o, ErrDataRetention
			}
			targets := []string{}
			for target := range metadata.Config.Volumes {
				targets = append(targets, target)
			}
			o.Images[image.ID] = targets
			for _, ref := range metadata.RepoDigests {
				o.Images[ref] = targets
			}
		}
		containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
		if err != nil {
			return o, err
		}
		for _, c := range containers.Items {
			inspection, err := cli.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
			if err != nil {
				return o, err
			}
			actual := inspection.Container
			o.Containers[c.ID] = actual
			if actual.Config == nil {
				return o, ErrDataRetention
			}
			image, err := cli.ImageInspect(ctx, actual.Image)
			if err != nil || image.Config == nil {
				return o, ErrDataRetention
			}
			targets := []string{}
			for target := range image.Config.Volumes {
				targets = append(targets, target)
			}
			o.Images[actual.Config.Image] = targets
			o.Images[actual.Image] = targets
			for _, ref := range image.RepoDigests {
				o.Images[ref] = targets
			}
		}
		return o, nil
	}
}
