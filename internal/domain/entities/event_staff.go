package entities

import "time"

const (
	EventStaffRoleEventManager = "event_manager"
	EventStaffRoleCheckInStaff = "checkin_staff"
)

type EventStaff struct {
	ID      int64 `json:"id" db:"id"`
	EventID int64 `json:"event_id" db:"event_id"`
	UserID  int64 `json:"user_id" db:"user_id"`

	Role     string `json:"role" db:"role"`
	IsActive bool   `json:"is_active" db:"is_active"`

	GrantedBy *int64 `json:"granted_by,omitempty" db:"granted_by"`
	RevokedBy *int64 `json:"revoked_by,omitempty" db:"revoked_by"`

	GrantedAt time.Time  `json:"granted_at" db:"granted_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

func (s *EventStaff) CanCheckIn() bool {
	if s == nil || !s.IsActive || s.RevokedAt != nil {
		return false
	}

	switch s.Role {
	case EventStaffRoleEventManager,
		EventStaffRoleCheckInStaff:
		return true
	default:
		return false
	}
}
