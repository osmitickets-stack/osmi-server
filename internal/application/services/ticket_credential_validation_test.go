package services

import (
	"context"

	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

type credentialTicketRepositoryStub struct {
	Ticket *entities.Ticket
	Err    error
}

func (r *credentialTicketRepositoryStub) GetByPublicID(
	ctx context.Context,
	publicID string,
) (*entities.Ticket, error) {
	if r.Err != nil {
		return nil, r.Err
	}

	if r.Ticket == nil ||
		r.Ticket.PublicID != publicID {
		return nil, repository.ErrTicketNotFound
	}

	return r.Ticket, nil
}
