package volumes

import (
	"context"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"hetzner.cloud/csi"
)

// IdempotentService wraps a volume service and provides idempotency as required by the CSI spec.
type IdempotentService struct {
	logger        log.Logger
	volumeService Service
}

func NewIdempotentService(logger log.Logger, volumeService Service) *IdempotentService {
	return &IdempotentService{
		logger:        logger,
		volumeService: volumeService,
	}
}

func (s *IdempotentService) Create(ctx context.Context, opts CreateOpts) (*csi.Volume, error) {
	level.Info(s.logger).Log(
		"msg", "creating volume",
		"name", opts.Name,
		"min-size", opts.MinSize,
		"max-size", opts.MaxSize,
		"location", opts.Location,
	)

	volume, err := s.volumeService.Create(ctx, opts)

	if err == nil {
		level.Info(s.logger).Log(
			"msg", "volume created",
			"volume-id", volume.ID,
		)
		return volume, nil
	}

	if err == ErrVolumeAlreadyExists {
		level.Info(s.logger).Log(
			"msg", "another volume with that name does already exist",
			"name", opts.Name,
		)
		existingVolume, err := s.volumeService.GetByName(ctx, opts.Name)
		if err != nil {
			level.Error(s.logger).Log(
				"msg", "failed to get existing volume",
				"name", opts.Name,
				"err", err,
			)
			return nil, err
		}
		if existingVolume == nil {
			level.Error(s.logger).Log(
				"msg", "existing volume disappeared",
				"name", opts.Name,
			)
			return nil, ErrVolumeAlreadyExists
		}
		if existingVolume.Size < opts.MinSize {
			level.Info(s.logger).Log(
				"msg", "existing volume is too small",
				"name", opts.Name,
				"min-size", opts.MinSize,
				"actual-size", existingVolume.Size,
			)
			return nil, ErrVolumeAlreadyExists
		}
		if opts.MaxSize > 0 && existingVolume.Size > opts.MaxSize {
			level.Info(s.logger).Log(
				"msg", "existing volume is too large",
				"name", opts.Name,
				"max-size", opts.MaxSize,
				"actual-size", existingVolume.Size,
			)
			return nil, ErrVolumeAlreadyExists
		}
		if existingVolume.Location != opts.Location {
			level.Info(s.logger).Log(
				"msg", "existing volume is in different location",
				"name", opts.Name,
				"location", opts.Location,
				"actual-location", existingVolume.Location,
			)
			return nil, ErrVolumeAlreadyExists
		}
		return existingVolume, nil
	}

	return nil, err
}

func (s *IdempotentService) GetByID(ctx context.Context, id uint64) (*csi.Volume, error) {
	return s.volumeService.GetByID(ctx, id)
}

func (s *IdempotentService) GetByName(ctx context.Context, name string) (*csi.Volume, error) {
	return s.volumeService.GetByName(ctx, name)
}

func (s *IdempotentService) Delete(ctx context.Context, volume *csi.Volume) error {
	_ = s.volumeService.Detach(ctx, volume)
	switch err := s.volumeService.Delete(ctx, volume); err {
	case ErrVolumeNotFound:
		return nil
	case nil:
		return nil
	default:
		return err
	}
}

func (s *IdempotentService) Attach(ctx context.Context, volume *csi.Volume, server *csi.Server) error {
	vol, err := s.volumeService.GetByID(ctx, volume.ID)
	if err != nil {
		return err
	}

	if vol.Server != nil && vol.Server.ID != server.ID {
		level.Info(s.logger).Log("msg", "Detaching volume",
			"volume-id", volume.ID,
			"server-id", server.ID,
		)
		err := s.volumeService.Detach(ctx, volume)

		level.Info(s.logger).Log("msg", "Detaching is done",
			"volume-id", volume.ID,
			"server-id", server.ID,
			"err", err,
		)
	}

	attachErr := s.volumeService.Attach(ctx, volume, server)
	if attachErr == nil {
		return nil
	}

	if vol.Server != nil && vol.Server.ID == server.ID {
		return nil
	}

	return attachErr
}

func (s *IdempotentService) Detach(ctx context.Context, volume *csi.Volume) error {
	switch err := s.volumeService.Detach(ctx, volume); err {
	case ErrNotAttached:
		return nil
	case nil:
		return nil
	default:
		return err
	}
}
