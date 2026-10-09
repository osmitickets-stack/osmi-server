// internal/application/services/ticket_service.go
package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	commondto "github.com/osmitickets-stack/osmi-server/internal/api/dto/common"
	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/enums"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

var (
	ErrTicketCredentialRequired = errors.New(
		"ticket credential is required",
	)

	ErrTicketCredentialInvalid = errors.New(
		"ticket credential is invalid",
	)

	ErrTicketCredentialTicketNotFound = errors.New(
		"ticket referenced by credential was not found",
	)
)

type TicketService struct {
	ticketRepo       repository.TicketRepository
	ticketTypeRepo   repository.TicketTypeRepository
	eventRepo        repository.EventRepository
	customerRepo     repository.CustomerRepository
	orderRepo        repository.OrderRepository
	ticketCredential *security.TicketCredentialService
}

func NewTicketService(
	ticketRepo repository.TicketRepository,
	ticketTypeRepo repository.TicketTypeRepository,
	eventRepo repository.EventRepository,
	customerRepo repository.CustomerRepository,
	orderRepo repository.OrderRepository,
	ticketCredential *security.TicketCredentialService,
) *TicketService {
	return &TicketService{
		ticketRepo:       ticketRepo,
		ticketTypeRepo:   ticketTypeRepo,
		eventRepo:        eventRepo,
		customerRepo:     customerRepo,
		orderRepo:        orderRepo,
		ticketCredential: ticketCredential,
	}
}

// ReserveTicket reserva un ticket con bloqueo FOR UPDATE
func (s *TicketService) ReserveTicket(ctx context.Context, req *ticketdto.ReserveTicketRequest) (*entities.Ticket, error) {
	if req.TicketID == "" {
		return nil, errors.New("ticket_type_id is required")
	}

	quantity := 1

	// Iniciar transacción
	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	ticketType, err := s.ticketTypeRepo.FindByPublicID(ctx, req.TicketID)
	if err != nil {
		return nil, fmt.Errorf("ticket type not found: %w", err)
	}

	// 🔥 USAR BLOQUEO FOR UPDATE
	err = s.ticketTypeRepo.ReserveTicketWithLock(ctx, tx, ticketType.ID, quantity)
	if err != nil {
		return nil, err
	}

	event, err := s.eventRepo.GetByID(ctx, ticketType.EventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	if !event.AllowReservations {
		return nil, errors.New("event does not allow reservations")
	}

	if s.ticketCredential == nil {
		return nil, errors.New(
			"ticket credential service is not configured",
		)
	}

	now := time.Now()
	reservationExpiresAt := now.Add(15 * time.Minute)

	ticketPublicID := uuid.New().String()

	qrCredential, err := s.ticketCredential.Sign(
		ticketPublicID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to generate ticket credential: %w",
			err,
		)
	}

	ticket := &entities.Ticket{
		PublicID:             ticketPublicID,
		TicketTypeID:         ticketType.ID,
		EventID:              event.ID,
		CustomerID:           nil,
		Code:                 s.generateTicketCode(event.ID, ticketType.ID, 0),
		SecretHash:           uuid.New().String(),
		QRCodeData:           &qrCredential,
		Status:               string(enums.TicketStatusReserved),
		FinalPrice:           ticketType.GetFinalPrice(),
		Currency:             ticketType.Currency,
		TaxAmount:            ticketType.BasePrice * ticketType.TaxRate,
		ReservedAt:           &now,
		ReservationExpiresAt: &reservationExpiresAt,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if err := ticket.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ticket: %w", err)
	}

	err = s.ticketRepo.CreateTx(ctx, tx, ticket)
	if err != nil {
		return nil, fmt.Errorf("failed to create reservation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return ticket, nil
}

// CheckInTicket marca un ticket como usado
func (s *TicketService) CheckInTicket(ctx context.Context, req *ticketdto.CheckInTicketRequest) (*entities.Ticket, error) {
	if req.TicketID == "" {
		return nil, errors.New("ticket_id is required")
	}

	ticket, err := s.ticketRepo.GetByPublicID(ctx, req.TicketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if ticket.Status != string(enums.TicketStatusSold) {
		return nil, errors.New("ticket is not valid for check-in")
	}

	if ticket.CheckedInAt != nil {
		return nil, errors.New("ticket already checked in")
	}

	event, err := s.eventRepo.GetByID(ctx, ticket.EventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	now := time.Now()
	if now.Before(event.StartsAt.Add(-1 * time.Hour)) {
		return nil, errors.New("check-in not available yet")
	}
	if now.After(event.EndsAt.Add(2 * time.Hour)) {
		return nil, errors.New("check-in period has ended")
	}

	var validatorID *int64
	if req.CheckedBy != "" {
		// TODO: Validar validador cuando exista auth
	}

	err = s.ticketRepo.CheckIn(ctx, ticket.ID, req.Method, req.Location, validatorID)
	if err != nil {
		return nil, fmt.Errorf("check-in failed: %w", err)
	}

	updatedTicket, err := s.ticketRepo.GetByID(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("ticket checked in but retrieval failed: %w", err)
	}

	return updatedTicket, nil
}

// TransferTicket transfiere un ticket
func (s *TicketService) TransferTicket(ctx context.Context, req *ticketdto.TransferTicketRequest) (*entities.Ticket, error) {
	if req.TicketID == "" {
		return nil, errors.New("ticket_id is required")
	}
	if req.ToCustomerID == "" {
		return nil, errors.New("to_customer_id is required")
	}

	ticket, err := s.ticketRepo.GetByPublicID(ctx, req.TicketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	// Si se proporciona from_customer_id, validar ownership
	if req.FromCustomerID != "" {
		fromCustomer, err := s.customerRepo.GetByPublicID(ctx, req.FromCustomerID)
		if err != nil {
			return nil, fmt.Errorf("sender customer not found: %w", err)
		}
		if ticket.CustomerID == nil || *ticket.CustomerID != fromCustomer.ID {
			return nil, errors.New("ticket does not belong to sender")
		}
	}

	if !ticket.CanBeTransferred() {
		return nil, errors.New("ticket cannot be transferred")
	}

	toCustomer, err := s.customerRepo.GetByPublicID(ctx, req.ToCustomerID)
	if err != nil {
		return nil, fmt.Errorf("recipient customer not found: %w", err)
	}

	transferToken := req.Token
	if transferToken == "" {
		transferToken = uuid.New().String()
	}

	err = s.ticketRepo.Transfer(ctx, ticket.ID, toCustomer.ID, transferToken)
	if err != nil {
		return nil, fmt.Errorf("transfer failed: %w", err)
	}

	updatedTicket, err := s.ticketRepo.GetByID(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("ticket transferred but retrieval failed: %w", err)
	}

	return updatedTicket, nil
}

// GetTicketStats obtiene estadísticas de tickets para un evento
func (s *TicketService) GetTicketStats(ctx context.Context, eventID string) (*ticketdto.TicketStatsResponse, error) {
	event, err := s.eventRepo.GetByPublicID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	stats, err := s.ticketRepo.GetEventStats(ctx, event.PublicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket stats: %w", err)
	}

	checkInRate := 0.0
	if stats.SoldTickets > 0 {
		checkInRate = float64(stats.CheckedInTickets) / float64(stats.SoldTickets)
	}

	return &ticketdto.TicketStatsResponse{
		TotalTickets:     stats.TotalTickets,
		AvailableTickets: stats.AvailableTickets,
		SoldTickets:      stats.SoldTickets,
		ReservedTickets:  stats.ReservedTickets,
		CheckedInTickets: stats.CheckedInTickets,
		CancelledTickets: stats.CancelledTickets,
		RefundedTickets:  stats.RefundedTickets,
		TotalRevenue:     stats.TotalRevenue,
		AvgTicketPrice:   stats.AvgTicketPrice,
		CheckInRate:      checkInRate,
	}, nil
}

// GetTicket obtiene un ticket por su ID público
func (s *TicketService) GetTicket(ctx context.Context, ticketID string) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.GetByPublicID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}
	return ticket, nil
}

// GetTicketByCode obtiene un ticket por su código
func (s *TicketService) GetTicketByCode(ctx context.Context, code string) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.GetByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("ticket not found with code %s: %w", code, err)
	}
	return ticket, nil
}

// ListTickets lista tickets con filtros y paginación
func (s *TicketService) ListTickets(ctx context.Context, filter *ticketdto.TicketFilter, pagination commondto.Pagination) ([]*entities.Ticket, int64, error) {
	repoFilter := &repository.TicketFilter{
		Limit:  pagination.PageSize,
		Offset: (pagination.Page - 1) * pagination.PageSize,
	}

	if filter != nil {
		if filter.EventID != nil {
			repoFilter.EventID = filter.EventID
		}
		if filter.CustomerID != nil {
			repoFilter.CustomerID = filter.CustomerID
		}
		if filter.OrderID != nil {
			repoFilter.OrderID = filter.OrderID
		}
		if filter.Status != "" {
			status := enums.TicketStatus(filter.Status)
			if status.IsValid() {
				repoFilter.Status = []enums.TicketStatus{status}
			}
		}
		if filter.TicketTypeID != nil {
			repoFilter.TicketTypeID = filter.TicketTypeID
		}
		if filter.DateFrom != "" {
			if t, err := time.Parse(time.RFC3339, filter.DateFrom); err == nil {
				repoFilter.CreatedFrom = &t
			}
		}
		if filter.DateTo != "" {
			if t, err := time.Parse(time.RFC3339, filter.DateTo); err == nil {
				repoFilter.CreatedTo = &t
			}
		}
		if filter.Code != "" {
			repoFilter.Code = &filter.Code
		}
	}

	return s.ticketRepo.Find(ctx, repoFilter)
}

// GetTicketsByEvent obtiene todos los tickets de un evento
func (s *TicketService) GetTicketsByEvent(ctx context.Context, eventID string) ([]*entities.Ticket, error) {
	event, err := s.eventRepo.GetByPublicID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	filter := &repository.TicketFilter{
		EventID: &event.ID,
	}
	tickets, _, err := s.ticketRepo.Find(ctx, filter)
	return tickets, err
}

// GetTicketsByCustomer obtiene tickets de un cliente
func (s *TicketService) GetTicketsByCustomer(ctx context.Context, customerID string, filter *ticketdto.TicketFilter, pagination commondto.Pagination) ([]*entities.Ticket, int64, error) {
	customer, err := s.customerRepo.GetByPublicID(ctx, customerID)
	if err != nil {
		return nil, 0, fmt.Errorf("customer not found: %w", err)
	}

	repoFilter := &repository.TicketFilter{
		CustomerID: &customer.ID,
		Limit:      pagination.PageSize,
		Offset:     (pagination.Page - 1) * pagination.PageSize,
	}

	if filter != nil {
		if filter.Status != "" {
			status := enums.TicketStatus(filter.Status)
			if status.IsValid() {
				repoFilter.Status = []enums.TicketStatus{status}
			}
		}
		if filter.DateFrom != "" {
			if t, err := time.Parse(time.RFC3339, filter.DateFrom); err == nil {
				repoFilter.CreatedFrom = &t
			}
		}
		if filter.DateTo != "" {
			if t, err := time.Parse(time.RFC3339, filter.DateTo); err == nil {
				repoFilter.CreatedTo = &t
			}
		}
	}

	return s.ticketRepo.Find(ctx, repoFilter)
}

// UpdateTicket actualiza información de un ticket (incluyendo status)
func (s *TicketService) UpdateTicket(ctx context.Context, ticketID string, req *ticketdto.UpdateTicketRequest) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.GetByPublicID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if req.AttendeeName != nil {
		ticket.AttendeeName = req.AttendeeName
	}
	if req.AttendeeEmail != nil {
		ticket.AttendeeEmail = req.AttendeeEmail
	}
	if req.AttendeePhone != nil {
		ticket.AttendeePhone = req.AttendeePhone
	}

	// RESERVED → SOLD es una transición financiera.
	// Solo PaymentService.ProcessPaidOrder puede realizarla después
	// de que Stripe haya confirmado el pago.
	if req.Status != nil && *req.Status != ticket.Status {
		newStatus := enums.TicketStatus(*req.Status)

		if newStatus == enums.TicketStatusSold {
			return nil, fmt.Errorf(
				"ticket cannot be marked as sold through UpdateTicket",
			)
		}

		if !enums.CanTransitionTicket(
			enums.TicketStatus(ticket.Status),
			newStatus,
		) {
			return nil, fmt.Errorf(
				"invalid status transition from %s to %s",
				ticket.Status,
				*req.Status,
			)
		}

		now := time.Now()

		switch newStatus {
		case enums.TicketStatusCancelled:
			ticket.CancelledAt = &now
		case enums.TicketStatusRefunded:
			ticket.RefundedAt = &now
		}

		ticket.Status = *req.Status
	}

	ticket.UpdatedAt = time.Now()

	err = s.ticketRepo.Update(ctx, ticket)
	if err != nil {
		return nil, fmt.Errorf("failed to update ticket: %w", err)
	}

	return ticket, nil
}

// CancelTicket cancela un ticket
func (s *TicketService) CancelTicket(ctx context.Context, ticketID string) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.GetByPublicID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if !ticket.CanBeCancelled() {
		return nil, errors.New("ticket cannot be cancelled")
	}

	err = s.ticketRepo.Cancel(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel ticket: %w", err)
	}

	updatedTicket, err := s.ticketRepo.GetByID(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("ticket cancelled but retrieval failed: %w", err)
	}

	return updatedTicket, nil
}

// RefundTicket reembolsa un ticket
func (s *TicketService) RefundTicket(ctx context.Context, ticketID string) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.GetByPublicID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if !ticket.CanBeRefunded() {
		return nil, errors.New("ticket cannot be refunded")
	}

	err = s.ticketRepo.Refund(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to refund ticket: %w", err)
	}

	updatedTicket, err := s.ticketRepo.GetByID(ctx, ticket.ID)
	if err != nil {
		return nil, fmt.Errorf("ticket refunded but retrieval failed: %w", err)
	}

	return updatedTicket, nil
}

// ValidateTicketCredential autentica una credencial QR emitida por OSMI.
//
// IMPORTANTE:
// Esta operación NO consume el ticket.
// No modifica status, checked_in_at ni ningún otro estado.
// Solamente:
//  1. verifica criptográficamente la credencial;
//  2. extrae el public UUID;
//  3. carga el ticket actual desde PostgreSQL.
func (s *TicketService) ValidateTicketCredential(
	ctx context.Context,
	credential string,
) (*entities.Ticket, error) {
	credential = strings.TrimSpace(credential)

	if credential == "" {
		return nil, ErrTicketCredentialRequired
	}

	if s.ticketCredential == nil {
		return nil, errors.New(
			"ticket credential service is not configured",
		)
	}

	ticketPublicID, err := s.ticketCredential.Verify(
		credential,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %v",
			ErrTicketCredentialInvalid,
			err,
		)
	}

	ticket, err := s.ticketRepo.GetByPublicID(
		ctx,
		ticketPublicID,
	)
	if err != nil {
		if errors.Is(err, repository.ErrTicketNotFound) {
			return nil, ErrTicketCredentialTicketNotFound
		}

		return nil, fmt.Errorf(
			"failed to load ticket from credential: %w",
			err,
		)
	}

	if ticket == nil {
		return nil, ErrTicketCredentialTicketNotFound
	}

	// Defensa de consistencia.
	//
	// El UUID obtenido de la firma debe coincidir exactamente
	// con el ticket recuperado de PostgreSQL.
	if ticket.PublicID != ticketPublicID {
		return nil, ErrTicketCredentialInvalid
	}

	return ticket, nil
}

// ValidateTicket valida un ticket por código y hash
func (s *TicketService) ValidateTicket(ctx context.Context, code, secretHash string) (*entities.Ticket, error) {
	ticket, err := s.ticketRepo.ValidateTicket(ctx, code, secretHash)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket: %w", err)
	}
	return ticket, nil
}

// generateTicketCode genera un código único para el ticket usando UUID
func (s *TicketService) generateTicketCode(eventID, ticketTypeID int64, attempt int) string {
	return fmt.Sprintf("TKT-%d-%d-%s", eventID, ticketTypeID, uuid.New().String()[:8])
}

// ReleaseExpiredReservations libera todas las reservas expiradas
func (s *TicketService) ReleaseExpiredReservations(ctx context.Context) (int64, error) {
	// 🔥 Iniciar transacción
	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Liberar reservas expiradas
	count, err := s.ticketTypeRepo.ReleaseExpiredReservationsTx(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("failed to release expired reservations: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ Liberadas %d reservas expiradas", count)
	return count, nil
}

func (s *TicketService) GetCustomerIDByUserID(ctx context.Context, userID string) (int64, error) {
	customers, _, err := s.customerRepo.Find(ctx, &repository.CustomerFilter{
		UserID: stringToInt64Ptr(userID),
		Limit:  1,
	})
	if err != nil || len(customers) == 0 {
		return 0, fmt.Errorf("customer not found for user %s", userID)
	}
	return customers[0].ID, nil
}

func stringToInt64Ptr(s string) *int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func (s *TicketService) VerifyTicketCredential(
	ctx context.Context,
	credential string,
) (*ticketdto.TicketCredentialValidationResponse, error) {
	ticket, err := s.ValidateTicketCredential(
		ctx,
		credential,
	)
	if err != nil {
		return nil, err
	}

	result := &ticketdto.TicketCredentialValidationResponse{
		Authentic:       true,
		CanCheckIn:      false,
		Result:          "VALID_CREDENTIAL",
		TicketPublicID:  ticket.PublicID,
		TicketCode:      ticket.Code,
		EventID:         ticket.EventID,
		TicketTypeID:    ticket.TicketTypeID,
		Status:          ticket.Status,
		CheckedInAt:     ticket.CheckedInAt,
		LastValidatedAt: ticket.LastValidatedAt,
	}

	switch ticket.Status {
	case string(enums.TicketStatusSold):
		result.CanCheckIn = true
		result.Result = "VALID"

	case string(enums.TicketStatusCheckedIn):
		result.Result = "ALREADY_CHECKED_IN"

	case string(enums.TicketStatusReserved):
		result.Result = "NOT_PAID"

	case string(enums.TicketStatusRefunded):
		result.Result = "REFUNDED"

	case string(enums.TicketStatusCancelled):
		result.Result = "CANCELLED"

	default:
		result.Result = "NOT_ELIGIBLE"
	}

	return result, nil
}
