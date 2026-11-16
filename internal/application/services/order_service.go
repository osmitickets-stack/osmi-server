package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	orderdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/order"
	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
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

	if req == nil || strings.TrimSpace(req.CustomerEmail) == "" {
		return nil, nil, fmt.Errorf("customer email is required")
	}
	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to start transaction: %w",
			err,
		)
	}
	defer tx.Rollback(ctx)
	// The client-provided customer_id is never an ownership proof. The
	// gateway forwards only a validated user identity over trusted mTLS.
	// Guest orders deliberately have no customer profile association.
	var customerID *int64
	var accountName string
	if userPublicID, authenticated := osmicontext.GetUserID(ctx); authenticated {
		var id int64
		var accountEmail string
		err = tx.QueryRow(ctx, `
			SELECT c.id, c.full_name, u.email::text
			FROM auth.users u JOIN crm.customers c ON c.user_id=u.id
			WHERE u.public_uuid=$1 AND u.is_active AND c.is_active
		`, userPublicID).Scan(&id, &accountName, &accountEmail)
		if err == pgx.ErrNoRows {
			return nil, nil, fmt.Errorf("authenticated customer profile is unavailable")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("load authenticated customer profile: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(req.CustomerEmail), strings.TrimSpace(accountEmail)) {
			return nil, nil, fmt.Errorf("use the email address of your signed-in account")
		}
		customerID = &id
	}

	var totalAmount float64
	var tickets []*entities.Ticket
	var orderItems []*entities.OrderItem

	// Every checkout acquires inventory locks in the same order. Two buyers
	// selecting the same types in reverse UI order must not deadlock.
	items := append([]orderdto.CreateOrderItemRequest(nil), req.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].TicketTypeID < items[j].TicketTypeID
	})
	for _, item := range items {
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
				CustomerID:   customerID,
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
		customerName = accountName
	}
	if customerName == "" {
		customerName = "Cliente OSMI"
	}

	order := &entities.Order{
		CustomerID:       customerID,
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
