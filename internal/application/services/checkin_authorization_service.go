package services

import (
	"context"
	"errors"
	"fmt"

	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

var (
	ErrCheckInAuthenticationRequired = errors.New(
		"authenticated user is required for check-in",
	)

	ErrCheckInUserInactive = errors.New(
		"authenticated user is inactive",
	)

	ErrCheckInPermissionDenied = errors.New(
		"user is not authorized to check in tickets for this event",
	)
)

type CheckInPrincipal struct {
	UserID       int64
	UserPublicID string
	AccessRole   string
}

type CheckInAuthorizer interface {
	Authorize(
		ctx context.Context,
		eventPublicID string,
	) (*CheckInPrincipal, error)
}

type CheckInAuthorizationService struct {
	userRepo       repository.UserRepository
	eventRepo      repository.EventRepository
	organizerRepo  repository.OrganizerRepository
	eventStaffRepo repository.EventStaffRepository
}

func NewCheckInAuthorizationService(
	userRepo repository.UserRepository,
	eventRepo repository.EventRepository,
	organizerRepo repository.OrganizerRepository,
	eventStaffRepo repository.EventStaffRepository,
) *CheckInAuthorizationService {
	return &CheckInAuthorizationService{
		userRepo:       userRepo,
		eventRepo:      eventRepo,
		organizerRepo:  organizerRepo,
		eventStaffRepo: eventStaffRepo,
	}
}

func (s *CheckInAuthorizationService) Authorize(
	ctx context.Context,
	eventPublicID string,
) (*CheckInPrincipal, error) {
	userPublicID, ok := osmicontext.GetUserID(ctx)
	if !ok || userPublicID == "" {
		return nil, ErrCheckInAuthenticationRequired
	}

	user, err := s.userRepo.GetByPublicID(
		ctx,
		userPublicID,
	)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, ErrCheckInAuthenticationRequired
		}

		return nil, fmt.Errorf(
			"failed to load authenticated user: %w",
			err,
		)
	}

	if user == nil {
		return nil, ErrCheckInAuthenticationRequired
	}

	if !user.IsActive {
		return nil, ErrCheckInUserInactive
	}

	event, err := s.eventRepo.GetByPublicID(
		ctx,
		eventPublicID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to load authorization event: %w",
			err,
		)
	}

	if event == nil {
		return nil, ErrCheckInPermissionDenied
	}

	// Superuser es un bypass global explícito.
	if user.IsSuperuser {
		return &CheckInPrincipal{
			UserID:       user.ID,
			UserPublicID: user.PublicID,
			AccessRole:   "superuser",
		}, nil
	}

	// El owner deriva de la relación existente:
	// user -> organizer -> event.
	if event.OrganizerID != nil {
		organizer, organizerErr := s.organizerRepo.FindByID(
			ctx,
			*event.OrganizerID,
		)
		if organizerErr != nil {
			return nil, fmt.Errorf(
				"failed to load event organizer: %w",
				organizerErr,
			)
		}

		if organizer != nil &&
			organizer.IsActive &&
			organizer.ApprovalStatus == "approved" &&
			organizer.UserID != nil &&
			*organizer.UserID == user.ID {
			return &CheckInPrincipal{
				UserID:       user.ID,
				UserPublicID: user.PublicID,
				AccessRole:   "owner",
			}, nil
		}
	}

	staff, err := s.eventStaffRepo.FindActiveByEventAndUser(
		ctx,
		event.ID,
		user.ID,
	)
	if err != nil {
		if errors.Is(
			err,
			repository.ErrEventStaffNotFound,
		) {
			return nil, ErrCheckInPermissionDenied
		}

		return nil, fmt.Errorf(
			"failed to resolve event staff authorization: %w",
			err,
		)
	}

	if staff == nil || !staff.CanCheckIn() {
		return nil, ErrCheckInPermissionDenied
	}

	return &CheckInPrincipal{
		UserID:       user.ID,
		UserPublicID: user.PublicID,
		AccessRole:   staff.Role,
	}, nil
}
