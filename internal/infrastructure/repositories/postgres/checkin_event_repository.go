package postgres

import (
	"context"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
)

// Keyset pagination bounds work without scanning all events in the application.
// Ownership/assignment only narrows candidates; CheckInAuthorizationService is
// still the authority for active users, approved owners and permitted staff roles.
func (r *EventRepository) ListCheckInCandidates(ctx context.Context, userID int64, superuser bool, cursor string, limit int) ([]*entities.Event, error) {
	if cursor == "" {
		cursor = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := r.db.Query(ctx, `
 SELECT e.id, e.public_uuid::text, e.name, e.starts_at
 FROM ticketing.events e
 WHERE e.public_uuid > $3::uuid AND (
   $2::boolean OR
   EXISTS (SELECT 1 FROM ticketing.organizers o WHERE o.id=e.organizer_id AND o.user_id=$1) OR
   EXISTS (SELECT 1 FROM ticketing.event_staff s WHERE s.event_id=e.id AND s.user_id=$1)
 )
 ORDER BY e.public_uuid LIMIT $4`, userID, superuser, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]*entities.Event, 0)
	for rows.Next() {
		event := &entities.Event{}
		if err := rows.Scan(&event.ID, &event.PublicID, &event.Name, &event.StartsAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
