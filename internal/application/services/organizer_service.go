package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	commondto "github.com/osmitickets-stack/osmi-server/internal/api/dto/common"
	organizerdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/organizer"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

var (
	ErrOrganizerRequired = errors.New(
		"organizer is required",
	)

	ErrOrganizerPublicIDRequired = errors.New(
		"organizer public_id is required",
	)

	ErrOrganizerSlugRequired = errors.New(
		"organizer slug is required",
	)

	ErrOrganizerInvalidVerificationStatus = errors.New(
		"invalid organizer verification status",
	)
)

type OrganizerService struct {
	organizerRepo repository.OrganizerRepository
}

func NewOrganizerService(
	organizerRepo repository.OrganizerRepository,
) *OrganizerService {
	return &OrganizerService{
		organizerRepo: organizerRepo,
	}
}

func (s *OrganizerService) Create(
	ctx context.Context,
	organizer *entities.Organizer,
) (*entities.Organizer, error) {
	if organizer == nil {
		return nil, ErrOrganizerRequired
	}

	organizer.Name = strings.TrimSpace(
		organizer.Name,
	)
	organizer.Slug = strings.TrimSpace(
		organizer.Slug,
	)
	organizer.ContactEmail = strings.TrimSpace(
		organizer.ContactEmail,
	)

	if err := organizer.Validate(); err != nil {
		return nil, fmt.Errorf(
			"invalid organizer: %w",
			err,
		)
	}

	if err := s.organizerRepo.Create(
		ctx,
		organizer,
	); err != nil {
		return nil, fmt.Errorf(
			"failed to create organizer: %w",
			err,
		)
	}

	return organizer, nil
}

func (s *OrganizerService) GetByID(
	ctx context.Context,
	id int64,
) (*entities.Organizer, error) {
	if id <= 0 {
		return nil, errors.New(
			"organizer id is required",
		)
	}

	organizer, err := s.organizerRepo.FindByID(
		ctx,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get organizer by id: %w",
			err,
		)
	}

	return organizer, nil
}

func (s *OrganizerService) GetByPublicID(
	ctx context.Context,
	publicID string,
) (*entities.Organizer, error) {
	publicID = strings.TrimSpace(publicID)

	if publicID == "" {
		return nil, ErrOrganizerPublicIDRequired
	}

	organizer, err := s.organizerRepo.FindByPublicID(
		ctx,
		publicID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get organizer by public id: %w",
			err,
		)
	}

	return organizer, nil
}

func (s *OrganizerService) GetBySlug(
	ctx context.Context,
	slug string,
) (*entities.Organizer, error) {
	slug = strings.TrimSpace(slug)

	if slug == "" {
		return nil, ErrOrganizerSlugRequired
	}

	organizer, err := s.organizerRepo.FindBySlug(
		ctx,
		slug,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get organizer by slug: %w",
			err,
		)
	}

	return organizer, nil
}

func (s *OrganizerService) List(
	ctx context.Context,
	filter organizerdto.OrganizerFilter,
	pagination commondto.Pagination,
) ([]*entities.Organizer, int64, error) {
	organizers, total, err := s.organizerRepo.List(
		ctx,
		filter,
		pagination,
	)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"failed to list organizers: %w",
			err,
		)
	}

	return organizers, total, nil
}

func (s *OrganizerService) Update(
	ctx context.Context,
	organizer *entities.Organizer,
) (*entities.Organizer, error) {
	if organizer == nil {
		return nil, ErrOrganizerRequired
	}

	if organizer.ID <= 0 {
		return nil, errors.New(
			"organizer id is required",
		)
	}

	organizer.Name = strings.TrimSpace(
		organizer.Name,
	)
	organizer.Slug = strings.TrimSpace(
		organizer.Slug,
	)
	organizer.ContactEmail = strings.TrimSpace(
		organizer.ContactEmail,
	)

	if err := organizer.Validate(); err != nil {
		return nil, fmt.Errorf(
			"invalid organizer: %w",
			err,
		)
	}

	if err := s.organizerRepo.Update(
		ctx,
		organizer,
	); err != nil {
		return nil, fmt.Errorf(
			"failed to update organizer: %w",
			err,
		)
	}

	return organizer, nil
}

func (s *OrganizerService) SoftDelete(
	ctx context.Context,
	publicID string,
) error {
	publicID = strings.TrimSpace(publicID)

	if publicID == "" {
		return ErrOrganizerPublicIDRequired
	}

	if err := s.organizerRepo.SoftDelete(
		ctx,
		publicID,
	); err != nil {
		return fmt.Errorf(
			"failed to deactivate organizer: %w",
			err,
		)
	}

	return nil
}

func (s *OrganizerService) Verify(
	ctx context.Context,
	publicID string,
) (*entities.Organizer, error) {
	organizer, err := s.GetByPublicID(
		ctx,
		publicID,
	)
	if err != nil {
		return nil, err
	}

	if err := s.organizerRepo.UpdateVerification(
		ctx,
		organizer.ID,
		true,
		"verified",
	); err != nil {
		return nil, fmt.Errorf(
			"failed to verify organizer: %w",
			err,
		)
	}

	organizer.IsVerifiedField = true
	organizer.VerificationStatus = "verified"

	return organizer, nil
}

func (s *OrganizerService) Reject(
	ctx context.Context,
	publicID string,
) (*entities.Organizer, error) {
	organizer, err := s.GetByPublicID(
		ctx,
		publicID,
	)
	if err != nil {
		return nil, err
	}

	if err := s.organizerRepo.UpdateVerification(
		ctx,
		organizer.ID,
		false,
		"rejected",
	); err != nil {
		return nil, fmt.Errorf(
			"failed to reject organizer: %w",
			err,
		)
	}

	organizer.IsVerifiedField = false
	organizer.VerificationStatus = "rejected"

	return organizer, nil
}

func (s *OrganizerService) SetVerificationStatus(
	ctx context.Context,
	publicID string,
	verificationStatus string,
) (*entities.Organizer, error) {
	verificationStatus = strings.TrimSpace(
		verificationStatus,
	)

	switch verificationStatus {
	case "pending", "verified", "rejected":
	default:
		return nil, ErrOrganizerInvalidVerificationStatus
	}

	organizer, err := s.GetByPublicID(
		ctx,
		publicID,
	)
	if err != nil {
		return nil, err
	}

	verified := verificationStatus == "verified"

	if err := s.organizerRepo.UpdateVerification(
		ctx,
		organizer.ID,
		verified,
		verificationStatus,
	); err != nil {
		return nil, fmt.Errorf(
			"failed to update organizer verification: %w",
			err,
		)
	}

	organizer.IsVerifiedField = verified
	organizer.VerificationStatus = verificationStatus

	return organizer, nil
}
