//internal/application/services/payment_service.go

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	paymentdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/payment"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/email"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/qr"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
	"github.com/stripe/stripe-go/v83"
	"github.com/stripe/stripe-go/v83/webhook"
)

type PaymentService struct {
	paymentRepo      repository.PaymentRepository
	orderRepo        repository.OrderRepository
	ticketRepo       repository.TicketRepository
	ticketTypeRepo   repository.TicketTypeRepository
	eventRepo        repository.EventRepository
	stripeClient     stripeIntentClient
	webhookSecret    string
	emailClient      *email.SESClient
	ticketCredential *security.TicketCredentialService
}

type stripeIntentClient interface {
	CreatePaymentIntent(amount int64, currency, orderID string, attempt int) (*stripe.PaymentIntent, error)
	GetPaymentIntent(id string) (*stripe.PaymentIntent, error)
}

type OrderConfirmationItem struct {
	TicketTypeID   string
	TicketTypeName string
	Quantity       int
	UnitPrice      float64
	TotalPrice     float64
	EventName      string
}

type OrderConfirmation struct {
	OrderID       string
	OrderStatus   string
	PaymentStatus string
	CustomerEmail string
	CustomerName  string
	TotalAmount   float64
	Currency      string
	Items         []OrderConfirmationItem
}

func (s *PaymentService) GetOrderConfirmation(
	ctx context.Context,
	orderID string,
	paymentIntentID string,
) (*OrderConfirmation, error) {
	if orderID == "" {
		return nil, fmt.Errorf("order_id is required")
	}
	if paymentIntentID == "" {
		return nil, fmt.Errorf("payment_intent_id is required")
	}

	paymentEntity, err := s.paymentRepo.FindByTransactionID(ctx, paymentIntentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}

	order, err := s.orderRepo.FindByPublicID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("order not found: %w", err)
	}

	if paymentEntity.OrderID != order.ID {
		return nil, fmt.Errorf("payment does not belong to order")
	}

	items, err := s.orderRepo.GetItems(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get order items: %w", err)
	}

	confirmationItems := make([]OrderConfirmationItem, 0, len(items))

	for _, item := range items {
		ticketType, err := s.ticketTypeRepo.FindByID(ctx, item.TicketTypeID)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get ticket type %d: %w",
				item.TicketTypeID,
				err,
			)
		}

		eventName := ""
		if ticketType != nil {
			event, err := s.eventRepo.GetByID(ctx, ticketType.EventID)
			if err == nil && event != nil {
				eventName = event.Name
			}
		}

		confirmationItems = append(
			confirmationItems,
			OrderConfirmationItem{
				TicketTypeID:   ticketType.PublicID,
				TicketTypeName: ticketType.Name,
				Quantity:       item.Quantity,
				UnitPrice:      item.UnitPrice,
				TotalPrice:     item.TotalPrice,
				EventName:      eventName,
			},
		)
	}

	customerName := ""
	if order.CustomerName != nil {
		customerName = *order.CustomerName
	}

	return &OrderConfirmation{
		OrderID:       order.PublicID,
		OrderStatus:   order.Status,
		PaymentStatus: order.PaymentStatus,
		CustomerEmail: order.CustomerEmail,
		CustomerName:  customerName,
		TotalAmount:   order.TotalAmount,
		Currency:      order.Currency,
		Items:         confirmationItems,
	}, nil
}

func NewPaymentService(
	paymentRepo repository.PaymentRepository,
	orderRepo repository.OrderRepository,
	ticketRepo repository.TicketRepository,
	ticketTypeRepo repository.TicketTypeRepository,
	eventRepo repository.EventRepository,
	stripeClient stripeIntentClient,
	webhookSecret string,
	emailClient *email.SESClient,
	ticketCredential ...*security.TicketCredentialService,
) *PaymentService {
	service := &PaymentService{
		paymentRepo:    paymentRepo,
		orderRepo:      orderRepo,
		ticketRepo:     ticketRepo,
		ticketTypeRepo: ticketTypeRepo,
		eventRepo:      eventRepo,
		stripeClient:   stripeClient,
		webhookSecret:  webhookSecret,
		emailClient:    emailClient,
	}
	if len(ticketCredential) > 0 {
		service.ticketCredential = ticketCredential[0]
	}
	return service
}

func strPtr(s string) *string {
	return &s
}

func (s *PaymentService) CreatePayment(
	ctx context.Context,
	req *paymentdto.CreatePaymentRequest,
) (*paymentdto.PaymentProcessingResponse, error) {
	intent, err := s.CreatePaymentIntent(ctx, &paymentdto.CreatePaymentIntentRequest{
		OrderID: req.OrderID, Currency: req.Currency,
	})
	if err != nil {
		return nil, err
	}
	paymentEntity, err := s.paymentRepo.FindByTransactionID(ctx, intent.PaymentIntentID)
	if err != nil {
		return nil, fmt.Errorf("failed to find payment after intent creation: %w", err)
	}
	return &paymentdto.PaymentProcessingResponse{
		PaymentID:      fmt.Sprintf("%d", paymentEntity.ID),
		Status:         paymentEntity.Status,
		RequiresAction: true,
		ActionType:     strPtr("stripe_sdk"),
		ProviderInstructions: map[string]interface{}{
			"client_secret":     intent.ClientSecret,
			"payment_intent_id": intent.PaymentIntentID,
		},
	}, nil
}
func (s *PaymentService) GetPayment(
	ctx context.Context,
	paymentID string,
) (*entities.Payment, error) {

	var id int64
	if _, err := fmt.Sscanf(paymentID, "%d", &id); err == nil {
		return s.paymentRepo.FindByID(ctx, id)
	}
	return s.paymentRepo.FindByTransactionID(ctx, paymentID)
}

func (s *PaymentService) HandleWebhook(
	ctx context.Context,
	payload []byte,
	signatureHeader string,
) error {
	event, err := webhook.ConstructEventWithOptions(
		payload,
		signatureHeader,
		s.webhookSecret,
		webhook.ConstructEventOptions{
			IgnoreAPIVersionMismatch: true,
		},
	)
	if err != nil {
		return fmt.Errorf("invalid webhook signature: %w", err)
	}

	// Registrar el evento antes de procesarlo.
	//
	// SaveStripeEvent:
	//   true  -> evento insertado ahora
	//   false -> event_id ya existía
	//
	// La existencia del evento NO significa que haya terminado
	// satisfactoriamente. processed_at es la fuente de verdad.
	inserted, err := s.paymentRepo.SaveStripeEvent(
		ctx,
		event.ID,
		string(event.Type),
		event.Data.Raw,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to persist stripe event %s: %w",
			event.ID,
			err,
		)
	}

	if !inserted {
		processed, err := s.paymentRepo.IsStripeEventProcessed(ctx, event.ID)
		if err != nil {
			return fmt.Errorf(
				"failed to check stripe event %s processing status: %w",
				event.ID,
				err,
			)
		}

		if processed {
			log.Printf(
				"ℹ️ Stripe event already processed: %s",
				event.ID,
			)
			return nil
		}

		log.Printf(
			"ℹ️ Retrying incomplete Stripe event: %s",
			event.ID,
		)
	}

	switch event.Type {
	case "payment_intent.succeeded":
		var paymentIntent stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
			return fmt.Errorf(
				"failed to parse payment intent: %w",
				err,
			)
		}

		paymentEntity, err := s.paymentRepo.FindByTransactionID(
			ctx,
			paymentIntent.ID,
		)
		if err != nil {
			return fmt.Errorf(
				"payment not found for transaction %s: %w",
				paymentIntent.ID,
				err,
			)
		}

		// Un pago reembolsado no debe volver a entrar al fulfillment.
		if paymentEntity.Status == "refunded" {
			log.Printf(
				"ℹ️ Ignoring payment_intent.succeeded for refunded payment: %s",
				paymentIntent.ID,
			)
			break
		}

		orderPublicID := paymentIntent.Metadata["order_id"]
		if orderPublicID == "" {
			return fmt.Errorf(
				"order_id not found in payment intent metadata",
			)
		}

		now := time.Now()

		// IMPORTANTE:
		// payment=completed NO significa que el fulfillment terminó.
		// Un retry puede encontrar:
		//
		// payment = completed
		// order.payment_status = paid
		// order.status = pending
		//
		// y debe continuar hasta ProcessPaidOrder.
		if paymentEntity.Status != "completed" {
			paymentEntity.Status = "completed"
			paymentEntity.ProcessedAt = &now
			paymentEntity.UpdatedAt = now

			if err := s.paymentRepo.Update(ctx, paymentEntity); err != nil {
				return fmt.Errorf(
					"failed to update payment: %w",
					err,
				)
			}
		}

		order, err := s.orderRepo.FindByPublicID(
			ctx,
			orderPublicID,
		)
		if err != nil {
			return fmt.Errorf(
				"order not found for public_id %s: %w",
				orderPublicID,
				err,
			)
		}

		if order.PaymentStatus != "paid" {
			order.PaymentStatus = "paid"
			order.PaidAt = &now
			order.UpdatedAt = now

			if err := s.orderRepo.Update(ctx, order); err != nil {
				return fmt.Errorf(
					"failed to update order payment status: %w",
					err,
				)
			}
		}

		// Fulfillment síncrono.
		//
		// No responder éxito al webhook hasta que:
		// RESERVED -> SOLD
		// inventory -> confirmed
		// order -> COMPLETED
		//
		// hayan terminado correctamente.
		if err := s.ProcessPaidOrder(ctx, orderPublicID); err != nil {
			return fmt.Errorf(
				"failed to fulfill paid order %s: %w",
				orderPublicID,
				err,
			)
		}

	case "payment_intent.payment_failed", "payment_intent.canceled":
		var paymentIntent stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
			return fmt.Errorf(
				"failed to parse failed payment intent: %w",
				err,
			)
		}

		paymentEntity, err := s.paymentRepo.FindByTransactionID(
			ctx,
			paymentIntent.ID,
		)
		if err != nil {
			return fmt.Errorf(
				"payment not found for failed transaction %s: %w",
				paymentIntent.ID,
				err,
			)
		}

		// A failed payment attempt can be retried on the same Stripe intent.
		// Only a canceled intent is safe to release from inventory.
		if event.Type == "payment_intent.canceled" &&
			paymentEntity.Status != "completed" &&
			paymentEntity.Status != "refunded" {

			paymentEntity.Status = "failed"
			now := time.Now()
			paymentEntity.CancelledAt = &now
			paymentEntity.UpdatedAt = now

			if err := s.paymentRepo.Update(ctx, paymentEntity); err != nil {
				return fmt.Errorf(
					"failed to update failed payment: %w",
					err,
				)
			}
		}

	default:
		// Los eventos que OSMI no necesita manejar también se consideran
		// procesados después de haber sido verificados y registrados.
	}

	if err := s.paymentRepo.MarkStripeEventProcessed(
		ctx,
		event.ID,
	); err != nil {
		return fmt.Errorf(
			"failed to mark stripe event %s as processed: %w",
			event.ID,
			err,
		)
	}

	return nil
}

func (s *PaymentService) ProcessPaidOrder(
	ctx context.Context,
	orderID string,
) error {

	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	defer tx.Rollback(ctx)

	order, err := s.orderRepo.FindByPublicIDForUpdate(
		ctx,
		tx,
		orderID,
	)
	if err != nil {
		return fmt.Errorf("order not found: %w", err)
	}

	// ========================================================
	// IDEMPOTENCY
	// ========================================================

	if order.Status == "completed" {

		log.Printf(
			"ℹ️ Order already completed: %s",
			order.PublicID,
		)

		return tx.Commit(ctx)
	}

	if order.PaymentStatus != "paid" {
		return fmt.Errorf(
			"order payment not confirmed yet",
		)
	}

	if order.Status != "pending" {
		return fmt.Errorf(
			"order cannot be processed, current status: %s",
			order.Status,
		)
	}

	items, err := s.orderRepo.GetItemsTx(
		ctx,
		tx,
		order.ID,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to get order items: %w",
			err,
		)
	}

	// ========================================================
	// PROCESS TICKETS
	// ========================================================
	// Buscar tickets asociados a esta orden directamente
	orderTickets, err := s.ticketRepo.FindByOrderIDForUpdate(
		ctx,
		tx,
		order.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to get tickets for order: %w", err)
	}

	// ========================================================
	// VALIDATE FULFILLMENT
	// ========================================================

	if len(items) == 0 {
		return fmt.Errorf("order %s has no order items", order.PublicID)
	}

	if len(orderTickets) == 0 {
		return fmt.Errorf("order %s has no tickets", order.PublicID)
	}

	// Cantidad que la orden espera por ticket_type_id.
	expectedByType := make(map[int64]int)

	for _, item := range items {
		if item.Quantity <= 0 {
			return fmt.Errorf(
				"invalid quantity %d for ticket type %d in order %s",
				item.Quantity,
				item.TicketTypeID,
				order.PublicID,
			)
		}

		expectedByType[item.TicketTypeID] += item.Quantity
	}

	// Cantidad real de tickets RESERVED por ticket_type_id.
	reservedByType := make(map[int64]int)

	for _, ticket := range orderTickets {
		if ticket == nil {
			return fmt.Errorf(
				"nil ticket found in order %s",
				order.PublicID,
			)
		}

		if ticket.Status != "reserved" {
			return fmt.Errorf(
				"ticket %s in order %s has invalid fulfillment status %q; expected reserved",
				ticket.PublicID,
				order.PublicID,
				ticket.Status,
			)
		}

		if _, exists := expectedByType[ticket.TicketTypeID]; !exists {
			return fmt.Errorf(
				"ticket %s references unexpected ticket type %d in order %s",
				ticket.PublicID,
				ticket.TicketTypeID,
				order.PublicID,
			)
		}

		reservedByType[ticket.TicketTypeID]++
	}

	// Validar exactamente lo comprado contra los tickets reservados.
	for ticketTypeID, expectedQuantity := range expectedByType {
		reservedQuantity := reservedByType[ticketTypeID]

		if reservedQuantity != expectedQuantity {
			return fmt.Errorf(
				"ticket quantity mismatch for order %s, ticket type %d: expected %d reserved tickets, found %d",
				order.PublicID,
				ticketTypeID,
				expectedQuantity,
				reservedQuantity,
			)
		}
	}

	if len(reservedByType) != len(expectedByType) {
		return fmt.Errorf(
			"ticket type mismatch in order %s",
			order.PublicID,
		)
	}

	// ========================================================
	// RESERVED -> SOLD
	// ========================================================

	now := time.Now()
	if s.ticketCredential == nil {
		return fmt.Errorf("cannot fulfill order %s without a ticket credential signer", order.PublicID)
	}

	for _, ticket := range orderTickets {
		if ticket.QRCodeData == nil || strings.TrimSpace(*ticket.QRCodeData) == "" {
			credential, err := s.ticketCredential.Sign(ticket.PublicID)
			if err != nil {
				return fmt.Errorf("failed to sign ticket %s: %w", ticket.PublicID, err)
			}
			ticket.QRCodeData = &credential
		} else {
			publicID, err := s.ticketCredential.Verify(*ticket.QRCodeData)
			if err != nil || publicID != ticket.PublicID {
				return fmt.Errorf("ticket %s has an invalid signed credential", ticket.PublicID)
			}
		}
		ticket.Status = "sold"
		ticket.SoldAt = &now
		ticket.ReservedAt = nil
		ticket.ReservationExpiresAt = nil
		ticket.UpdatedAt = now

		if err := s.ticketRepo.UpdateTx(ctx, tx, ticket); err != nil {
			return fmt.Errorf(
				"failed to mark ticket %s as sold: %w",
				ticket.PublicID,
				err,
			)
		}
	}

	// ========================================================
	// CONFIRM INVENTORY
	// ========================================================

	// Bloquear/actualizar ticket types siempre en orden determinista.
	// Esto reduce el riesgo de deadlocks entre checkouts concurrentes.
	ticketTypeIDs := make([]int64, 0, len(expectedByType))

	for ticketTypeID := range expectedByType {
		ticketTypeIDs = append(ticketTypeIDs, ticketTypeID)
	}

	sort.Slice(ticketTypeIDs, func(i, j int) bool {
		return ticketTypeIDs[i] < ticketTypeIDs[j]
	})

	for _, ticketTypeID := range ticketTypeIDs {
		quantity := expectedByType[ticketTypeID]

		if err := s.ticketTypeRepo.ConfirmReservationTx(
			ctx,
			tx,
			ticketTypeID,
			quantity,
		); err != nil {
			return fmt.Errorf(
				"failed to confirm reservation for ticket type %d, quantity %d: %w",
				ticketTypeID,
				quantity,
				err,
			)
		}
	}

	// ========================================================
	// COMPLETE ORDER (DENTRO DE LA TRANSACCIÓN)
	// ========================================================

	order.Status = "completed"
	order.UpdatedAt = time.Now()

	_, err = tx.Exec(ctx, `
		UPDATE billing.orders 
		SET status = $1,
		    updated_at = NOW()
		WHERE public_uuid = $2
	`,
		order.Status,
		order.PublicID,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to update order: %w",
			err,
		)
	}

	// ========================================================
	// COMMIT
	// ========================================================

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf(
			"failed to commit transaction: %w",
			err,
		)
	}

	// ========================================================
	// RECARGAR ORDEN DESPUÉS DEL COMMIT
	// ========================================================

	freshOrder, err := s.orderRepo.FindByPublicID(
		ctx,
		order.PublicID,
	)
	if err == nil && freshOrder != nil {
		order = freshOrder
	}

	log.Printf(
		"✅ Order processed successfully: %s",
		order.PublicID,
	)

	// ========================================================
	// SEND TICKETS EMAIL AFTER COMMIT
	// ========================================================

	if err := s.sendOrderTicketsEmail(
		ctx,
		order,
		orderTickets,
	); err != nil {
		log.Printf(
			"⚠️ Ticket email delivery failed for order %s: %v",
			order.PublicID,
			err,
		)
	} else {
		log.Printf("📧 Ticket email accepted by SMTP for order %s", order.PublicID)
	}

	return nil
}

func (s *PaymentService) sendOrderTicketsEmail(
	ctx context.Context,
	order *entities.Order,
	orderTickets []*entities.Ticket,
) error {
	if s.emailClient == nil {
		return fmt.Errorf("email client is not configured")
	}

	if order == nil {
		return fmt.Errorf("order is nil")
	}

	if order.CustomerEmail == "" {
		return fmt.Errorf(
			"order %s has no customer email",
			order.PublicID,
		)
	}

	if len(orderTickets) == 0 {
		return fmt.Errorf(
			"order %s has no tickets to email",
			order.PublicID,
		)
	}

	customerName := "Cliente osmi"

	if order.CustomerName != nil &&
		*order.CustomerName != "" {
		customerName = *order.CustomerName
	}

	emailTickets := make(
		[]email.TicketEmailItem,
		0,
		len(orderTickets),
	)

	for _, ticket := range orderTickets {
		if ticket == nil {
			return fmt.Errorf(
				"order %s contains a nil ticket",
				order.PublicID,
			)
		}

		if ticket.Status != "sold" {
			return fmt.Errorf(
				"ticket %s is not sold; current status: %s",
				ticket.PublicID,
				ticket.Status,
			)
		}

		if ticket.Code == "" {
			return fmt.Errorf(
				"ticket %s has empty code",
				ticket.PublicID,
			)
		}

		ticketType, err := s.ticketTypeRepo.FindByID(
			ctx,
			ticket.TicketTypeID,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to load ticket type %d for ticket %s: %w",
				ticket.TicketTypeID,
				ticket.PublicID,
				err,
			)
		}

		if ticketType == nil {
			return fmt.Errorf(
				"ticket type %d not found for ticket %s",
				ticket.TicketTypeID,
				ticket.PublicID,
			)
		}

		event, err := s.eventRepo.GetByID(
			ctx,
			ticket.EventID,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to load event %d for ticket %s: %w",
				ticket.EventID,
				ticket.PublicID,
				err,
			)
		}

		if event == nil {
			return fmt.Errorf(
				"event %d not found for ticket %s",
				ticket.EventID,
				ticket.PublicID,
			)
		}

		eventLocation := ""

		if event.VenueName != nil {
			eventLocation = *event.VenueName
		}

		if ticket.QRCodeData == nil || strings.TrimSpace(*ticket.QRCodeData) == "" {
			return fmt.Errorf(
				"ticket %s has no QR credential",
				ticket.PublicID,
			)
		}

		qrBase64, err := qr.GenerateQRBase64(
			*ticket.QRCodeData,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to generate QR for ticket %s: %w",
				ticket.PublicID,
				err,
			)
		}

		emailTickets = append(
			emailTickets,
			email.TicketEmailItem{
				TicketCode:     ticket.Code,
				TicketTypeName: ticketType.Name,
				EventName:      event.Name,
				EventDate: event.StartsAt.Format(
					"02/01/2006 15:04",
				),
				EventLocation: eventLocation,
				QRBase64:      qrBase64,
			},
		)
	}

	return s.emailClient.SendOrderTicketsEmail(
		order.CustomerEmail,
		customerName,
		order.PublicID,
		emailTickets,
	)
}

func (s *PaymentService) CreatePaymentIntent(
	ctx context.Context,
	req *paymentdto.CreatePaymentIntentRequest,
) (*paymentdto.CreatePaymentIntentResponse, error) {
	if req == nil || req.OrderID == "" {
		return nil, fmt.Errorf("order_id is required")
	}
	if s.stripeClient == nil {
		return nil, fmt.Errorf("Stripe client is not configured")
	}
	tx, err := s.ticketRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start payment transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Expiration and fulfillment acquire the order lock first. Keep it until
	// the provider intent and local payment row are both committed.
	order, err := s.orderRepo.FindByPublicIDForUpdate(ctx, tx, req.OrderID)
	if err != nil {
		return nil, fmt.Errorf("order not found: %w", err)
	}
	if order.Status != "pending" || order.PaymentStatus == "paid" {
		return nil, fmt.Errorf("order is not payable")
	}
	currency := req.Currency
	if currency == "" {
		currency = "MXN"
	}
	if !strings.EqualFold(currency, order.Currency) {
		return nil, fmt.Errorf("payment currency does not match order")
	}

	// A retry for the same order reuses its active intent. Concurrent
	// requests serialize on the order lock, so they cannot create two.
	var activeID, activeStatus string
	err = tx.QueryRow(ctx, `
		SELECT provider_transaction_id, status FROM billing.payments
		WHERE order_id=$1 AND status IN ('processing','failed','completed')
		  AND provider_transaction_id IS NOT NULL
		ORDER BY CASE status WHEN 'completed' THEN 0 WHEN 'processing' THEN 1 ELSE 2 END, id DESC LIMIT 1
	`, order.ID).Scan(&activeID, &activeStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check active payment: %w", err)
	}
	if activeID != "" {
		if activeStatus == "completed" {
			return nil, fmt.Errorf("order payment is already completed")
		}
		pi, err := s.stripeClient.GetPaymentIntent(activeID)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve active payment intent: %w", err)
		}
		if pi == nil {
			return nil, fmt.Errorf("active payment intent is unavailable")
		}
		if string(pi.Status) == "canceled" {
			if _, err := tx.Exec(ctx, `
				UPDATE billing.payments SET status='failed', cancelled_at=NOW(), updated_at=NOW()
				WHERE order_id=$1 AND provider_transaction_id=$2
				  AND status IN ('processing','failed')
			`, order.ID, activeID); err != nil {
				return nil, fmt.Errorf("failed to mark canceled payment: %w", err)
			}
		} else {
			if pi.ClientSecret == "" {
				return nil, fmt.Errorf("active payment intent is unavailable")
			}
			if string(pi.Status) == "succeeded" || string(pi.Status) == "processing" {
				return nil, fmt.Errorf("order payment is awaiting settlement")
			}
			if activeStatus == "failed" {
				if _, err := tx.Exec(ctx, `UPDATE billing.payments SET status='processing', updated_at=NOW() WHERE order_id=$1 AND provider_transaction_id=$2 AND status='failed'`, order.ID, activeID); err != nil {
					return nil, fmt.Errorf("failed to resume payment intent: %w", err)
				}
			}
			return &paymentdto.CreatePaymentIntentResponse{
				ClientSecret: pi.ClientSecret, PaymentIntentID: pi.ID,
				Amount: pi.Amount, Currency: string(pi.Currency),
			}, tx.Commit(ctx)
		}
	}

	var total, live int
	err = tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (
			WHERE status='reserved' AND reservation_expires_at > NOW()
		) FROM ticketing.tickets WHERE order_id=$1
	`, order.ID).Scan(&total, &live)
	if err != nil {
		return nil, fmt.Errorf("failed to check order reservation: %w", err)
	}
	if total == 0 || live != total {
		return nil, fmt.Errorf("order reservation has expired or is unavailable")
	}

	var providerID int16
	if err := tx.QueryRow(ctx, `SELECT id FROM billing.payment_providers WHERE code='stripe' AND is_active=true`).Scan(&providerID); err != nil {
		return nil, fmt.Errorf("active Stripe payment provider is not configured: %w", err)
	}
	var attempt int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM billing.payments WHERE order_id=$1 AND cancelled_at IS NOT NULL`, order.ID).Scan(&attempt); err != nil {
		return nil, fmt.Errorf("failed to count canceled payment attempts: %w", err)
	}
	pi, err := s.stripeClient.CreatePaymentIntent(int64(order.TotalAmount*100), currency, order.PublicID, attempt)
	if err != nil {
		return nil, fmt.Errorf("failed to create Stripe payment intent: %w", err)
	}
	if pi == nil || pi.ID == "" || pi.ClientSecret == "" {
		return nil, fmt.Errorf("Stripe returned an incomplete payment intent")
	}
	now := time.Now()
	paymentEntity := &entities.Payment{
		OrderID: order.ID, ProviderID: providerID, ProviderTransactionID: &pi.ID,
		Amount: order.TotalAmount, Currency: currency, ExchangeRate: 1,
		Status: "processing", PaymentMethod: strPtr("card"),
		MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.paymentRepo.CreateTx(ctx, tx, paymentEntity); err != nil {
		return nil, fmt.Errorf("failed to persist payment intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit payment intent: %w", err)
	}
	return &paymentdto.CreatePaymentIntentResponse{
		ClientSecret: pi.ClientSecret, PaymentIntentID: pi.ID,
		Amount: pi.Amount, Currency: string(pi.Currency),
	}, nil
}
