package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

type EventStaffRepository struct {
	db *pgxpool.Pool
}

func NewEventStaffRepository(
	db *pgxpool.Pool,
) *EventStaffRepository {
	return &EventStaffRepository{
		db: db,
	}
}

func (r *EventStaffRepository) FindActiveByEventAndUser(
	ctx context.Context,
	eventID int64,
	userID int64,
) (*entities.EventStaff, error) {
	const query = `
		SELECT
			id,
			event_id,
			user_id,
			role,
			is_active,
			granted_by,
			revoked_by,
			granted_at,
			revoked_at,
			created_at,
			updated_at
		FROM ticketing.event_staff
		WHERE event_id = $1
		  AND user_id = $2
		  AND is_active = true
		  AND revoked_at IS NULL
		LIMIT 1
	`

	var staff entities.EventStaff

	err := r.db.QueryRow(
		ctx,
		query,
		eventID,
		userID,
	).Scan(
		&staff.ID,
		&staff.EventID,
		&staff.UserID,
		&staff.Role,
		&staff.IsActive,
		&staff.GrantedBy,
		&staff.RevokedBy,
		&staff.GrantedAt,
		&staff.RevokedAt,
		&staff.CreatedAt,
		&staff.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrEventStaffNotFound
		}

		return nil, fmt.Errorf(
			"failed to find active event staff: %w",
			err,
		)
	}

	return &staff, nil
}

var _ repository.EventStaffRepository = (*EventStaffRepository)(nil)
