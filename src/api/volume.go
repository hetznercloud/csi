package api

import (
	"context"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hetznercloud/hcloud-go/hcloud"

	"hetzner.cloud/csi"
	"hetzner.cloud/csi/volumes"
)

type VolumeService struct {
	logger log.Logger
	client *hcloud.Client
}

func NewVolumeService(logger log.Logger, client *hcloud.Client) *VolumeService {
	return &VolumeService{
		logger: logger,
		client: client,
	}
}

func (s *VolumeService) Create(ctx context.Context, opts volumes.CreateOpts) (*csi.Volume, error) {
	level.Info(s.logger).Log(
		"msg", "creating volume",
		"volume-name", opts.Name,
		"volume-size", opts.MinSize,
		"volume-location", opts.Location,
	)

	result, _, err := s.client.Volume.Create(ctx, hcloud.VolumeCreateOpts{
		Name:     opts.Name,
		Size:     opts.MinSize,
		Location: &hcloud.Location{Name: opts.Location},
	})
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to create volume",
			"volume-name", opts.Name,
			"err", err,
		)
		if hcloud.IsError(err, hcloud.ErrorCode("uniqueness_error")) {
			return nil, volumes.ErrVolumeAlreadyExists
		}
		return nil, err
	}

	_, errCh := s.client.Action.WatchProgress(ctx, result.Action)
	if err := <-errCh; err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to create volume",
			"volume-name", opts.Name,
			"err", err,
		)
		_, _ = s.client.Volume.Delete(ctx, result.Volume) // fire and forget
		return nil, err
	}

	return toDomainVolume(result.Volume), nil
}

func (s *VolumeService) GetByID(ctx context.Context, id uint64) (*csi.Volume, error) {
	hcloudVolume, _, err := s.client.Volume.GetByID(ctx, int(id))
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to get volume",
			"volume-id", id,
			"err", err,
		)
		return nil, err
	}
	if hcloudVolume == nil {
		level.Info(s.logger).Log(
			"msg", "volume not found",
			"volume-id", id,
		)
		return nil, volumes.ErrVolumeNotFound
	}
	return toDomainVolume(hcloudVolume), nil
}

func (s *VolumeService) GetByName(ctx context.Context, name string) (*csi.Volume, error) {
	hcloudVolume, _, err := s.client.Volume.GetByName(ctx, name)
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to get volume",
			"volume-name", name,
			"err", err,
		)
		return nil, err
	}
	if hcloudVolume == nil {
		level.Info(s.logger).Log(
			"msg", "volume not found",
			"volume-name", name,
		)
		return nil, volumes.ErrVolumeNotFound
	}
	return toDomainVolume(hcloudVolume), nil
}

func (s *VolumeService) Delete(ctx context.Context, volume *csi.Volume) error {
	level.Info(s.logger).Log(
		"msg", "deleting volume",
		"volume-id", volume.ID,
	)

	hcloudVolume := &hcloud.Volume{ID: int(volume.ID)}
	_, err := s.client.Volume.Delete(ctx, hcloudVolume)
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to delete volume",
			"volume-id", volume.ID,
			"err", err,
		)
		if hcloud.IsError(err, hcloud.ErrorCodeNotFound) {
			return volumes.ErrVolumeNotFound
		}
		return err
	}
	return nil
}

func (s *VolumeService) Attach(ctx context.Context, volume *csi.Volume, server *csi.Server) error {
	level.Info(s.logger).Log(
		"msg", "attaching volume",
		"volume-id", volume.ID,
		"server-id", server.ID,
	)

	hcloudVolume, _, err := s.client.Volume.GetByID(ctx, int(volume.ID))
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to get volume",
			"volume-id", volume.ID,
			"err", err,
		)
		return err
	}
	if hcloudVolume == nil {
		level.Info(s.logger).Log(
			"msg", "volume to attach not found",
			"volume-id", volume.ID,
		)
		return volumes.ErrVolumeNotFound
	}

	hcloudServer, _, err := s.client.Server.GetByID(ctx, int(server.ID))
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to get server",
			"volume-id", volume.ID,
			"server-id", server.ID,
			"err", err,
		)
		return err
	}
	if hcloudServer == nil {
		level.Info(s.logger).Log(
			"msg", "server to attach volume to not found",
			"volume-id", volume.ID,
			"server-id", server.ID,
		)
		return volumes.ErrServerNotFound
	}

	action, _, err := s.client.Volume.Attach(ctx, hcloudVolume, hcloudServer)
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to attach volume",
			"volume-id", volume.ID,
			"server-id", server.ID,
			"err", err,
		)
		if hcloud.IsError(err, hcloud.ErrorCode("limit_exceeded_error")) {
			return volumes.ErrAttachLimitReached
		}
		return err
	}

	_, errCh := s.client.Action.WatchProgress(ctx, action)
	if err := <-errCh; err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to attach volume",
			"volume-id", volume.ID,
			"server-id", server.ID,
			"err", err,
		)
		return err
	}
	return nil
}

func (s *VolumeService) Detach(ctx context.Context, volume *csi.Volume, server *csi.Server) error {
	level.Info(s.logger).Log(
		"msg", "detaching volume",
		"volume-id", volume.ID,
		"server-id", server.ID,
	)

	hcloudVolume, _, err := s.client.Volume.GetByID(ctx, int(volume.ID))
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to get volume to detach",
			"volume-id", volume.ID,
			"err", err,
		)
		return err
	}
	if hcloudVolume == nil {
		level.Info(s.logger).Log(
			"msg", "volume to detach not found",
			"volume-id", volume.ID,
			"err", err,
		)
		return volumes.ErrVolumeNotFound
	}
	if hcloudVolume.Server == nil {
		level.Info(s.logger).Log(
			"msg", "volume not attached to a server",
			"volume-id", volume.ID,
		)
		return volumes.ErrNotAttached
	}

	// If a server is provided, only detach if the volume is actually attached
	// to that server.
	if server != nil {
		hcloudServer, _, err := s.client.Server.GetByID(ctx, int(server.ID))
		if err != nil {
			level.Info(s.logger).Log(
				"msg", "failed to get server to detach volume from",
				"volume-id", volume.ID,
				"server-id", server.ID,
				"err", err,
			)
			return err
		}
		if hcloudServer == nil {
			level.Info(s.logger).Log(
				"msg", "server to detach volume from not found",
				"volume-id", volume.ID,
				"server-id", server.ID,
				"err", err,
			)
			return volumes.ErrServerNotFound
		}
		if hcloudVolume.Server.ID != hcloudServer.ID {
			level.Info(s.logger).Log(
				"msg", "volume not attached to provided server",
				"volume-id", volume.ID,
				"server-id", server.ID,
				"attached-to-server-id", hcloudVolume.Server.ID,
			)
			return volumes.ErrAlreadyAttached
		}
	}

	action, _, err := s.client.Volume.Detach(ctx, hcloudVolume)
	if err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to detach volume",
			"volume-id", volume.ID,
			"err", err,
		)
		return err
	}

	_, errCh := s.client.Action.WatchProgress(ctx, action)
	if err := <-errCh; err != nil {
		level.Info(s.logger).Log(
			"msg", "failed to detach volume",
			"volume-id", volume.ID,
			"err", err,
		)
		return err
	}
	return nil
}
