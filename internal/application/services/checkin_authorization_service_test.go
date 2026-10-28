package services

import (
	"context"
	"errors"
	"testing"
	"time"

	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

// The embedded interfaces satisfy the full production repository contracts.
// Only the read methods used by CheckInAuthorizationService are overridden.
// This avoids changing production interfaces merely to make them mockable.

type authUserRepoStub struct {
	repository.UserRepository
	find func(context.Context, string) (*entities.User, error)
}

func (r authUserRepoStub) GetByPublicID(ctx context.Context, id string) (*entities.User, error) {
	return r.find(ctx, id)
}

type authEventRepoStub struct {
	repository.EventRepository
	find func(context.Context, string) (*entities.Event, error)
}

func (r authEventRepoStub) GetByPublicID(ctx context.Context, id string) (*entities.Event, error) {
	return r.find(ctx, id)
}

type authOrganizerRepoStub struct {
	repository.OrganizerRepository
	find func(context.Context, int64) (*entities.Organizer, error)
}

func (r authOrganizerRepoStub) FindByID(ctx context.Context, id int64) (*entities.Organizer, error) {
	return r.find(ctx, id)
}

type authStaffRepoStub struct {
	repository.EventStaffRepository
	find func(context.Context, int64, int64) (*entities.EventStaff, error)
}

func (r authStaffRepoStub) FindActiveByEventAndUser(ctx context.Context, eventID, userID int64) (*entities.EventStaff, error) {
	return r.find(ctx, eventID, userID)
}

type authTestFixture struct {
	ctx                                               context.Context
	user                                              *entities.User
	event                                             *entities.Event
	organizer                                         *entities.Organizer
	staff                                             *entities.EventStaff
	userErr, eventErr, organizerErr, staffErr         error
	staffNilWithoutError                              bool
	userCalls, eventCalls, organizerCalls, staffCalls int
}

const (
	authTestUserPublicID  = "11111111-1111-4111-8111-111111111111"
	authTestEventPublicID = "22222222-2222-4222-8222-222222222222"
)

func authInt64Ptr(v int64) *int64 { return &v }

func newAuthTestFixture() *authTestFixture {
	return &authTestFixture{
		ctx:       osmicontext.WithUserID(context.Background(), authTestUserPublicID),
		user:      &entities.User{ID: 42, PublicID: authTestUserPublicID, IsActive: true},
		event:     &entities.Event{ID: 88, PublicID: authTestEventPublicID, OrganizerID: authInt64Ptr(31)},
		organizer: &entities.Organizer{ID: 31, UserID: authInt64Ptr(999), IsActive: true, ApprovalStatus: "approved"},
		staff:     nil,
	}
}

func (f *authTestFixture) service() *CheckInAuthorizationService {
	return NewCheckInAuthorizationService(
		authUserRepoStub{find: func(_ context.Context, publicID string) (*entities.User, error) {
			f.userCalls++
			if f.userErr != nil {
				return nil, f.userErr
			}
			if f.user == nil || f.user.PublicID != publicID {
				return nil, repository.ErrUserNotFound
			}
			return f.user, nil
		}},
		authEventRepoStub{find: func(_ context.Context, publicID string) (*entities.Event, error) {
			f.eventCalls++
			if f.eventErr != nil {
				return nil, f.eventErr
			}
			if f.event == nil || f.event.PublicID != publicID {
				return nil, nil
			}
			return f.event, nil
		}},
		authOrganizerRepoStub{find: func(_ context.Context, id int64) (*entities.Organizer, error) {
			f.organizerCalls++
			if f.organizerErr != nil {
				return nil, f.organizerErr
			}
			if f.organizer == nil || f.organizer.ID != id {
				return nil, nil
			}
			return f.organizer, nil
		}},
		authStaffRepoStub{find: func(_ context.Context, eventID, userID int64) (*entities.EventStaff, error) {
			f.staffCalls++
			if f.staffErr != nil {
				return nil, f.staffErr
			}
			if f.staffNilWithoutError {
				return nil, nil
			}
			if f.staff == nil || f.staff.EventID != eventID || f.staff.UserID != userID {
				return nil, repository.ErrEventStaffNotFound
			}
			return f.staff, nil
		}},
	)
}

func (f *authTestFixture) assignStaff(role string) {
	f.staff = &entities.EventStaff{
		EventID:  88,
		UserID:   42,
		Role:     role,
		IsActive: true,
	}
}

func TestCheckInAuthorizationMatrix(t *testing.T) {
	dbFailure := errors.New("simulated database failure")
	cases := []struct {
		name             string
		configure        func(*authTestFixture)
		wantRole         string
		wantErr          error
		wantNoUserLookup bool
	}{
		{name: "guest without authenticated identity", configure: func(f *authTestFixture) { f.ctx = context.Background() }, wantErr: ErrCheckInAuthenticationRequired, wantNoUserLookup: true},
		{name: "unknown user", configure: func(f *authTestFixture) { f.user = nil }, wantErr: ErrCheckInAuthenticationRequired},
		{name: "user repository error fails closed", configure: func(f *authTestFixture) { f.userErr = dbFailure }, wantErr: dbFailure},
		{name: "inactive user", configure: func(f *authTestFixture) { f.user.IsActive = false }, wantErr: ErrCheckInUserInactive},
		{name: "unknown event", configure: func(f *authTestFixture) { f.event = nil }, wantErr: ErrCheckInPermissionDenied},
		{name: "event repository error fails closed", configure: func(f *authTestFixture) { f.eventErr = dbFailure }, wantErr: dbFailure},
		{name: "active superuser", configure: func(f *authTestFixture) { f.user.IsSuperuser = true }, wantRole: "superuser"},
		{name: "approved active owner", configure: func(f *authTestFixture) { f.organizer.UserID = authInt64Ptr(42) }, wantRole: "owner"},
		{name: "owner with unapproved organizer", configure: func(f *authTestFixture) {
			f.organizer.UserID = authInt64Ptr(42)
			f.organizer.ApprovalStatus = "pending"
		}, wantErr: ErrCheckInPermissionDenied},
		{name: "owner with inactive organizer", configure: func(f *authTestFixture) { f.organizer.UserID = authInt64Ptr(42); f.organizer.IsActive = false }, wantErr: ErrCheckInPermissionDenied},
		{name: "customer without event staff", wantErr: ErrCheckInPermissionDenied},
		{name: "checkin staff for correct event", configure: func(f *authTestFixture) { f.assignStaff("checkin_staff") }, wantRole: "checkin_staff"},
		{name: "event manager for correct event", configure: func(f *authTestFixture) { f.assignStaff("event_manager") }, wantRole: "event_manager"},
		{name: "staff assigned to different event", configure: func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.EventID = 999 }, wantErr: ErrCheckInPermissionDenied},
		{name: "staff assigned to different user", configure: func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.UserID = 999 }, wantErr: ErrCheckInPermissionDenied},
		{name: "inactive staff", configure: func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.IsActive = false }, wantErr: ErrCheckInPermissionDenied},
		{name: "revoked staff", configure: func(f *authTestFixture) {
			f.assignStaff("checkin_staff")
			revoked := time.Now()
			f.staff.RevokedAt = &revoked
		}, wantErr: ErrCheckInPermissionDenied},
		{name: "unknown staff role", configure: func(f *authTestFixture) { f.assignStaff("viewer") }, wantErr: ErrCheckInPermissionDenied},
		{name: "staff repository nil result", configure: func(f *authTestFixture) { f.staffNilWithoutError = true }, wantErr: ErrCheckInPermissionDenied},
		{name: "staff repository error fails closed", configure: func(f *authTestFixture) { f.staffErr = dbFailure }, wantErr: dbFailure},
		{name: "organizer repository error fails closed", configure: func(f *authTestFixture) { f.organizerErr = dbFailure }, wantErr: dbFailure},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthTestFixture()
			if tc.configure != nil {
				tc.configure(f)
			}
			principal, err := f.service().Authorize(f.ctx, authTestEventPublicID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v; got %v", tc.wantErr, err)
				}
				if principal != nil {
					t.Fatalf("denied authorization returned principal: %+v", principal)
				}
			} else {
				if err != nil {
					t.Fatalf("authorized request returned error: %v", err)
				}
				if principal == nil {
					t.Fatal("authorized request returned nil principal")
				}
				if principal.UserID != 42 || principal.UserPublicID != authTestUserPublicID || principal.AccessRole != tc.wantRole {
					t.Fatalf("unexpected principal: %+v", principal)
				}
			}
			if tc.wantNoUserLookup && f.userCalls != 0 {
				t.Fatalf("guest triggered %d user lookups", f.userCalls)
			}
		})
	}
}
