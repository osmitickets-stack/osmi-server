package services

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

var ErrCheckInCursorInvalid = errors.New("invalid check-in events cursor")

type CheckInEventsPage struct {
	Events     []*entities.Event
	NextCursor string
}

func (s *CheckInAuthorizationService) ListCheckInEvents(ctx context.Context, cursor string) (*CheckInEventsPage, error) {
	userID, ok := osmicontext.GetUserID(ctx)
	if !ok || userID == "" {
		return nil, ErrCheckInAuthenticationRequired
	}
	user, err := s.userRepo.GetByPublicID(ctx, userID)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, ErrCheckInAuthenticationRequired
	}
	if err != nil {
		return nil, fmt.Errorf("load scanner user: %w", err)
	}
	if user == nil || user.ID <= 0 {
		return nil, ErrCheckInAuthenticationRequired
	}
	if !user.IsActive {
		return nil, ErrCheckInUserInactive
	}
	if cursor != "" {
		id, err := uuid.Parse(cursor)
		if err != nil || id == uuid.Nil || id.String() != cursor {
			return nil, ErrCheckInCursorInvalid
		}
	}
	repo, ok := s.eventRepo.(repository.CheckInEventRepository)
	if !ok {
		return nil, errors.New("check-in event discovery is not configured")
	}
	const pageSize = 20
	candidates, err := repo.ListCheckInCandidates(ctx, user.ID, user.IsSuperuser, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list scanner candidates: %w", err)
	}
	page := &CheckInEventsPage{Events: make([]*entities.Event, 0)}
	for _, candidate := range candidates {
		if candidate == nil {
			return nil, errors.New("invalid check-in event candidate")
		}
	}
	if len(candidates) > pageSize {
		candidates = candidates[:pageSize]
		page.NextCursor = candidates[len(candidates)-1].PublicID
	}
	for _, event := range candidates {
		if event == nil {
			return nil, errors.New("invalid check-in event candidate")
		}
		principal, err := s.Authorize(ctx, event.PublicID)
		if errors.Is(err, ErrCheckInPermissionDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if principal == nil || principal.UserID != user.ID || principal.UserPublicID != user.PublicID || principal.AccessRole == "" {
			return nil, ErrCheckInPrincipalInvalid
		}
		page.Events = append(page.Events, event)
	}
	return page, nil
}

func (s *TicketService) ListCheckInEvents(ctx context.Context, cursor string) (*CheckInEventsPage, error) {
	lister, ok := s.checkInAuthorization.(interface {
		ListCheckInEvents(context.Context, string) (*CheckInEventsPage, error)
	})
	if !ok {
		return nil, errors.New("check-in event discovery is not configured")
	}
	return lister.ListCheckInEvents(ctx, cursor)
}
