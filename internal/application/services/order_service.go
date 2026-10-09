package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	orderdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/order"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

type OrderService struct {
	orderRepo        repository.OrderRepository
	customerRepo     repository.CustomerRepository
	ticketTypeRepo   repository.TicketTypeRepository
	ticketRepo       repository.TicketRepository
	ticketCredential *security.TicketCredentialService
}

func NewOrderService(
	orderRepo repository.OrderRepository,
	customerRepo repository.CustomerRepository,
	ticketTypeRepo repository.TicketTypeRepository,
	ticketRepo repository.TicketRepository,
	ticketCredential *security.TicketCredentialService,
) *OrderService {
	return &OrderService{
		orderRepo:        orderRepo,
		customerRepo:     customerRepo,
		ticketTypeRepo:   ticketTypeRepo,
		ticketRepo:       ticketRepo,
		ticketCredential: ticketCredential,
	}
}

// CreateOrder crea una orden con los items seleccionados.
func (s *OrderService) CreateOrder(
	ctx context.Context,
	req *orderdto.CreateOrderRequest,
) (*entities.Order, []*entities.Ticket, error) {
	if s.ticketCredential == nil {
		return nil, nil, fmt.Errorf(
			"ticket credential service is not configured",
		)
	}

	customer, err := s.customerRepo.GetByPublicID(
		ctx,
		req.CustomerID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"customer not found: %w",
			err,
		)
	}

	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to start transaction: %w",
			err,
		)
	}
	defer tx.Rollback(ctx)

	var totalAmount float64
	var tickets []*entities.Ticket
	var orderItems []*entities.OrderItem

	for _, item := range req.Items {
		ticketType, err := s.ticketTypeRepo.FindByPublicID(
			ctx,
			item.TicketTypeID,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"ticket type not found: %w",
				err,
			)
		}

		err = s.ticketTypeRepo.ReserveTicketWithLock(
			ctx,
			tx,
			ticketType.ID,
			item.Quantity,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"failed to reserve tickets: %w",
				err,
			)
		}

		unitPrice := ticketType.BasePrice
		itemTotal := unitPrice * float64(item.Quantity)

		orderItems = append(
			orderItems,
			&entities.OrderItem{
				TicketTypeID: ticketType.ID,
				Quantity:     item.Quantity,
				UnitPrice:    unitPrice,
				TotalPrice:   itemTotal,
			},
		)

		for i := 0; i < item.Quantity; i++ {
			ticketPublicID := uuid.New().String()

			qrCredential, err := s.ticketCredential.Sign(
				ticketPublicID,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to generate ticket credential: %w",
					err,
				)
			}

			now := time.Now()

			ticket := &entities.Ticket{
				PublicID:     ticketPublicID,
				TicketTypeID: ticketType.ID,
				EventID:      ticketType.EventID,
				CustomerID:   &customer.ID,
				Code: fmt.Sprintf(
					"ORD-%d-%d-%s",
					ticketType.EventID,
					ticketType.ID,
					uuid.New().String()[:8],
				),
				SecretHash:           uuid.New().String(),
				QRCodeData:           &qrCredential,
				Status:               "reserved",
				FinalPrice:           ticketType.BasePrice,
				Currency:             ticketType.Currency,
				TaxAmount:            ticketType.BasePrice * ticketType.TaxRate,
				ReservedAt:           &now,
				ReservationExpiresAt: timePtr(now.Add(15 * time.Minute)),
				CreatedAt:            now,
				UpdatedAt:            now,
			}

			err = s.ticketRepo.CreateTx(
				ctx,
				tx,
				ticket,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to create ticket: %w",
					err,
				)
			}

			tickets = append(
				tickets,
				ticket,
			)

			totalAmount += ticket.FinalPrice
		}
	}

	paymentMethodStr := ""

	customerName := strings.TrimSpace(req.CustomerName)
	if customerName == "" {
		customerName = customer.FullName
	}

	order := &entities.Order{
		CustomerID:       &customer.ID,
		CustomerEmail:    req.CustomerEmail,
		CustomerName:     &customerName,
		Subtotal:         totalAmount,
		TaxAmount:        0,
		ServiceFeeAmount: 0,
		DiscountAmount:   0,
		TotalAmount:      totalAmount,
		Currency:         "MXN",
		Status:           "pending",
		OrderType:        "ticket",
		PaymentMethod:    &paymentMethodStr,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	err = s.orderRepo.CreateTx(
		ctx,
		tx,
		order,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to create order: %w",
			err,
		)
	}

	for _, orderItem := range orderItems {
		orderItem.OrderID = order.ID

		if err := s.orderRepo.AddItemTx(
			ctx,
			tx,
			orderItem,
		); err != nil {
			return nil, nil, fmt.Errorf(
				"failed to create order item: %w",
				err,
			)
		}
	}

	for _, ticket := range tickets {
		ticket.OrderID = &order.ID

		err = s.ticketRepo.UpdateTx(
			ctx,
			tx,
			ticket,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"failed to associate ticket to order: %w",
				err,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf(
			"failed to commit transaction: %w",
			err,
		)
	}

	return order, tickets, nil
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// generateTicketCode genera un código único para el ticket.
func (s *OrderService) generateTicketCode(
	eventID,
	ticketTypeID int64,
	attempt int,
) string {
	return fmt.Sprintf(
		"ORD-%d-%d-%s",
		eventID,
		ticketTypeID,
		uuid.New().String()[:8],
	)
}
