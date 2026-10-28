package repository

import (
	"context"
	"errors"

	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
)

var ErrEventStaffNotFound = errors.New(
	"event staff assignment not found",
)

type EventStaffRepository interface {
	FindActiveByEventAndUser(
		ctx context.Context,
		eventID int64,
		userID int64,
	) (*entities.EventStaff, error)
}
