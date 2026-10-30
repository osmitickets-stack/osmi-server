package services

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"testing"
)

type discoveryEventRepo struct {
	repository.EventRepository
	candidates []*entities.Event
	calls      int
	userID     int64
	superuser  bool
	cursor     string
	limit      int
	err        error
}

func (r *discoveryEventRepo) ListCheckInCandidates(_ context.Context, userID int64, superuser bool, cursor string, limit int) ([]*entities.Event, error) {
	r.calls++
	r.userID = userID
	r.superuser = superuser
	r.cursor = cursor
	r.limit = limit
	return r.candidates, r.err
}

func TestListCheckInEventsAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*authTestFixture)
		want      int
		err       error
	}{
		{"guest", func(f *authTestFixture) { f.ctx = context.Background() }, 0, ErrCheckInAuthenticationRequired},
		{"inactive", func(f *authTestFixture) { f.user.IsActive = false }, 0, ErrCheckInUserInactive},
		{"customer", func(f *authTestFixture) {}, 0, nil},
		{"owner", func(f *authTestFixture) { f.organizer.UserID = authInt64Ptr(42) }, 1, nil},
		{"unapproved owner", func(f *authTestFixture) {
			f.organizer.UserID = authInt64Ptr(42)
			f.organizer.ApprovalStatus = "pending"
		}, 0, nil},
		{"superuser", func(f *authTestFixture) { f.user.IsSuperuser = true }, 1, nil},
		{"staff", func(f *authTestFixture) { f.assignStaff("checkin_staff") }, 1, nil},
		{"manager", func(f *authTestFixture) { f.assignStaff("event_manager") }, 1, nil},
		{"revoked", func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.IsActive = false }, 0, nil},
		{"other event", func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.EventID = 999 }, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthTestFixture()
			tc.configure(f)
			service := f.service()
			repo := &discoveryEventRepo{EventRepository: service.eventRepo, candidates: []*entities.Event{f.event}}
			service.eventRepo = repo
			page, err := service.ListCheckInEvents(f.ctx, "")
			if tc.err != nil {
				if !errors.Is(err, tc.err) || page != nil || repo.calls != 0 {
					t.Fatalf("page=%v err=%v queries=%d", page, err, repo.calls)
				}
				return
			}
			if err != nil || page == nil || len(page.Events) != tc.want {
				t.Fatalf("page=%v err=%v expected=%d", page, err, tc.want)
			}
			if repo.userID != 42 || repo.superuser != f.user.IsSuperuser || repo.limit != 21 {
				t.Fatal("discovery must use database identity and a bounded page")
			}
		})
	}
}

func TestListCheckInEventsPaginationAndFailures(t *testing.T) {
	f := newAuthTestFixture()
	service := f.service()
	repo := &discoveryEventRepo{EventRepository: service.eventRepo}
	service.eventRepo = repo
	for i := 0; i < 21; i++ {
		repo.candidates = append(repo.candidates, &entities.Event{PublicID: uuid.NewString()})
	}
	page, err := service.ListCheckInEvents(f.ctx, "")
	if err != nil || len(page.Events) != 0 || page.NextCursor != repo.candidates[19].PublicID {
		t.Fatalf("empty denied page must retain continuation: %v %v", page, err)
	}
	_, err = service.ListCheckInEvents(f.ctx, "invalid")
	if !errors.Is(err, ErrCheckInCursorInvalid) || repo.calls != 1 {
		t.Fatalf("invalid cursor: %v calls=%d", err, repo.calls)
	}
	repo.err = errors.New("database unavailable")
	page, err = service.ListCheckInEvents(f.ctx, "")
	if err == nil || page != nil {
		t.Fatal("repository failures must not become an empty success")
	}
}
