package grpc

import (
	"context"
	"testing"

	pb "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/application/services"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

type registrationUserRepository struct {
	repository.UserRepository
	created *entities.User
}

func (r *registrationUserRepository) GetByEmail(context.Context, string) (*entities.User, error) {
	return nil, repository.ErrUserNotFound
}

func (r *registrationUserRepository) Create(_ context.Context, user *entities.User) error {
	r.created = user
	user.ID = 1
	return nil
}

type registrationCustomerRepository struct{ repository.CustomerRepository }

func (*registrationCustomerRepository) Create(context.Context, *entities.Customer) error { return nil }

func TestPublicRegistrationCannotChoosePrivilegedRole(t *testing.T) {
	for _, requested := range []string{"", "customer", "staff", "admin"} {
		t.Run(requested, func(t *testing.T) {
			users := &registrationUserRepository{}
			service := services.NewUserService(users, &registrationCustomerRepository{}, nil, security.NewPasswordHasher(), nil, nil)
			handler := NewUserHandler(service, "unused-test-secret")
			response, err := handler.CreateUser(context.Background(), &pb.CreateUserRequest{
				Name: "Demo User", Email: "demo@example.invalid", Password: "throwaway-test-password", Role: requested,
			})
			if err != nil {
				t.Fatal(err)
			}
			if users.created == nil || users.created.IsStaff || users.created.IsSuperuser || response.GetRole() != "customer" {
				t.Fatalf("public registration granted role %q", requested)
			}
		})
	}
}
