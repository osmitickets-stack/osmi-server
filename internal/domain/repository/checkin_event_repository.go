package repository

import (
	"context"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
)

// Candidate discovery is not authorization. The application authorizes each row.
type CheckInEventRepository interface {
	ListCheckInCandidates(ctx context.Context, userID int64, superuser bool, cursor string, limit int) ([]*entities.Event, error)
}
