package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	paymentdto "github.com/franciscozamorau/osmi-server/internal/api/dto/payment"
	"github.com/franciscozamorau/osmi-server/internal/domain/entities"
	"github.com/franciscozamorau/osmi-server/internal/domain/repository"
	"github.com/franciscozamorau/osmi-server/internal/infrastructure/email"
	"github.com/franciscozamorau/osmi-server/internal/infrastructure/payment"
	"github.com/franciscozamorau/osmi-server/internal/infrastructure/qr"
	"github.com/stripe/stripe-go/v83"
	"github.com/stripe/stripe-go/v83/webhook"
)

type PaymentService struct {
	paymentRepo    repository.PaymentRepository
	orderRepo      repository.OrderRepository
	ticketRepo     repository.TicketRepository
	ticketTypeRepo repository.TicketTypeRepository
	eventRepo      repository.EventRepository
	stripeClient   *payment.StripeClient
	webhookSecret  string
	emailClient    *email.SESClient
}

func NewPaymentService(
	paymentRepo repository.PaymentRepository,
	orderRepo repository.OrderRepository,
	ticketRepo repository.TicketRepository,
	ticketTypeRepo repository.TicketTypeRepository,
	eventRepo repository.EventRepository,
	stripeClient *payment.StripeClient,
	webhookSecret string,
	emailClient *email.SESClient,
) *PaymentService {
	return &PaymentService{
		paymentRepo:    paymentRepo,
		orderRepo:      orderRepo,
		ticketRepo:     ticketRepo,
		ticketTypeRepo: ticketTypeRepo,
		eventRepo:      eventRepo,
		stripeClient:   stripeClient,
		webhookSecret:  webhookSecret,
		emailClient:    emailClient,
	}
}

func strPtr(s string) *string {
	return &s
}

func (s *PaymentService) CreatePayment(
	ctx context.Context,
	req *paymentdto.CreatePaymentRequest,
) (*paymentdto.PaymentProcessingResponse, error) {

	order, err := s.orderRepo.FindByPublicID(ctx, req.OrderID)
	if err != nil {
		return nil, fmt.Errorf("order not found: %w", err)
	}

	if order.Status != "pending" {
		return nil, fmt.Errorf("order is not pending, current status: %s", order.Status)
	}

	providerID := int16(1)
	now := time.Now()

	paymentEntity := &entities.Payment{
		OrderID:       order.ID,
		ProviderID:    providerID,
		Amount:        order.TotalAmount,
		Currency:      req.Currency,
		ExchangeRate:  1.0,
		Status:        "pending",
		PaymentMethod: &req.PaymentMethod,
		Attempts:      0,
		MaxAttempts:   3,
		IPAddress:     nil,
		UserAgent:     nil,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := paymentEntity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid payment: %w", err)
	}

	if err := s.paymentRepo.Create(ctx, paymentEntity); err != nil {
		return nil, fmt.Errorf("failed to create payment: %w", err)
	}

	amountCents := int64(order.TotalAmount * 100)

	pi, err := s.stripeClient.CreatePaymentIntent(amountCents, req.Currency, order.PublicID)
	if err != nil {
		paymentEntity.Status = "failed"
		_ = s.paymentRepo.Update(ctx, paymentEntity)
		return nil, fmt.Errorf("failed to create Stripe payment intent: %w", err)
	}

	paymentEntity.ProviderTransactionID = &pi.ID
	paymentEntity.Status = "processing"
	paymentEntity.UpdatedAt = time.Now()

	if err := s.paymentRepo.Update(ctx, paymentEntity); err != nil {
		return nil, fmt.Errorf("failed to update payment with Stripe data: %w", err)
	}

	paymentID := fmt.Sprintf("%d", paymentEntity.ID)

	return &paymentdto.PaymentProcessingResponse{
		PaymentID:      paymentID,
		Status:         paymentEntity.Status,
		RequiresAction: true,
		ActionType:     strPtr("stripe_sdk"),
		ProviderInstructions: map[string]interface{}{
			"client_secret":     pi.ClientSecret,
			"payment_intent_id": pi.ID,
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
		payload, signatureHeader, s.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true},
	)
	if err != nil {
		return fmt.Errorf("invalid webhook signature: %w", err)
	}

	if err := s.paymentRepo.SaveStripeEvent(ctx, event.ID, string(event.Type), event.Data.Raw); err != nil {
		log.Printf("⚠️ Error guardando stripe_event %s: %v", event.ID, err)
	}

	switch event.Type {

	case "payment_intent.succeeded":

		var paymentIntent stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
			return fmt.Errorf("failed to parse payment intent: %w", err)
		}

		paymentEntity, err := s.paymentRepo.FindByTransactionID(ctx, paymentIntent.ID)
		if err != nil {
			return fmt.Errorf("payment not found for transaction %s: %w", paymentIntent.ID, err)
		}

		if paymentEntity.Status == "completed" || paymentEntity.Status == "refunded" {
			log.Printf("ℹ️ Payment already processed: %s", paymentIntent.ID)
			return nil
		}

		now := time.Now()
		paymentEntity.Status = "completed"
		paymentEntity.ProcessedAt = &now
		paymentEntity.UpdatedAt = now

		if err := s.paymentRepo.Update(ctx, paymentEntity); err != nil {
			return fmt.Errorf("failed to update payment: %w", err)
		}

		orderPublicID := paymentIntent.Metadata["order_id"]
		if orderPublicID == "" {
			return fmt.Errorf("order_id not found in payment intent metadata")
		}

		order, err := s.orderRepo.FindByPublicID(ctx, orderPublicID)
		if err != nil {
			return fmt.Errorf("order not found for public_id %s: %w", orderPublicID, err)
		}

		order.PaymentStatus = "paid"
		order.UpdatedAt = now

		if err := s.orderRepo.Update(ctx, order); err != nil {
			return fmt.Errorf("failed to update order payment status: %w", err)
		}

		go func(orderID string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			if err := s.ProcessPaidOrder(bgCtx, orderID); err != nil {
				log.Printf("⚠️ Error procesando orden %s: %v", orderID, err)
			}
		}(orderPublicID)

		return nil

	case "payment_intent.payment_failed":

		var paymentIntent stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
			return fmt.Errorf("failed to parse failed payment intent: %w", err)
		}

		paymentEntity, err := s.paymentRepo.FindByTransactionID(ctx, paymentIntent.ID)
		if err != nil {
			return nil
		}

		if paymentEntity.Status == "completed" {
			return nil
		}

		paymentEntity.Status = "failed"
		paymentEntity.UpdatedAt = time.Now()

		if err := s.paymentRepo.Update(ctx, paymentEntity); err != nil {
			return fmt.Errorf("failed to update failed payment: %w", err)
		}

		return nil

	default:
		return nil
	}
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

	items, err := s.orderRepo.GetItems(
		ctx,
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
	orderTickets, _, err := s.ticketRepo.Find(ctx, &repository.TicketFilter{
		OrderID: &order.ID,
	})

	if err != nil {
		return fmt.Errorf("failed to get tickets for order: %w", err)
	}

	var firstTicketCode string

	for _, ticket := range orderTickets {
		if firstTicketCode == "" {
			firstTicketCode = ticket.Code
		}

		if ticket.Status != "reserved" {
			continue
		}

		now := time.Now()
		ticket.Status = "sold"
		ticket.SoldAt = &now
		ticket.ReservedAt = nil
		ticket.ReservationExpiresAt = nil
		ticket.UpdatedAt = now

		if err := s.ticketRepo.UpdateTx(ctx, tx, ticket); err != nil {
			return fmt.Errorf("failed to update ticket: %w", err)
		}

		if err := s.ticketTypeRepo.ConfirmReservationTx(ctx, tx, ticket.TicketTypeID, 1); err != nil {
			return fmt.Errorf("failed to confirm reservation: %w", err)
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
	// DEBUG EMAIL
	// ========================================================

	log.Printf(
		"DEBUG EMAIL -> emailClient nil? %v",
		s.emailClient == nil,
	)

	log.Printf(
		"DEBUG EMAIL -> customerEmail: '%s'",
		order.CustomerEmail,
	)

	// ========================================================
	// SEND EMAIL
	// ========================================================

	if s.emailClient != nil && order.CustomerEmail != "" {

		customerName := "Invitado"

		if order.CustomerName != nil {
			customerName = *order.CustomerName
		}

		eventName := "Tu evento en osmi"
		eventDate := ""
		eventLocation := ""
		ticketCode := firstTicketCode
		if ticketCode == "" {
			ticketCode = "N/A"
		}

		if len(items) > 0 {

			ticketType, err := s.ticketTypeRepo.FindByID(
				ctx,
				items[0].TicketTypeID,
			)
			if err == nil && ticketType != nil {

				event, err := s.eventRepo.GetByID(
					ctx,
					ticketType.EventID,
				)
				if err == nil && event != nil {

					eventName = event.Name

					eventDate = event.StartsAt.Format(
						"02/01/2006 15:04",
					)

					if event.VenueName != nil {
						eventLocation = *event.VenueName
					}
				}
			}
		}

		qrBase64, _ := qr.GenerateQRBase64(ticketCode)

		if err := s.emailClient.SendTicketEmail(
			order.CustomerEmail,
			customerName,
			eventName,
			eventDate,
			eventLocation,
			ticketCode,
			qrBase64,
		); err != nil {

			log.Printf(
				"⚠️ Error enviando email para orden %s: %v",
				order.PublicID,
				err,
			)

		} else {

			log.Printf(
				"📧 Email enviado a %s",
				order.CustomerEmail,
			)
		}
	}

	return nil
}

func (s *PaymentService) CreatePaymentIntent(
	ctx context.Context,
	req *paymentdto.CreatePaymentIntentRequest,
) (*paymentdto.CreatePaymentIntentResponse, error) {

	order, err := s.orderRepo.FindByPublicID(ctx, req.OrderID)
	if err != nil {
		return nil, fmt.Errorf("order not found: %w", err)
	}

	if order.Status != "pending" {
		return nil, fmt.Errorf("order is not pending, current status: %s", order.Status)
	}

	if order.PaymentStatus == "paid" {
		return nil, fmt.Errorf("order already paid")
	}

	currency := req.Currency
	if currency == "" {
		currency = "MXN"
	}

	amountCents := int64(order.TotalAmount * 100)

	pi, err := s.stripeClient.CreatePaymentIntent(amountCents, currency, order.PublicID)
	if err != nil {
		return nil, fmt.Errorf("failed to create stripe payment intent: %w", err)
	}

	now := time.Now()

	paymentEntity := &entities.Payment{
		OrderID:               order.ID,
		ProviderID:            1,
		ProviderTransactionID: &pi.ID,
		Amount:                order.TotalAmount,
		Currency:              currency,
		ExchangeRate:          1.0,
		Status:                "processing",
		PaymentMethod:         strPtr("card"),
		Attempts:              0,
		MaxAttempts:           3,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if err := s.paymentRepo.Create(ctx, paymentEntity); err != nil {
		return nil, fmt.Errorf("failed to persist payment record: %w", err)
	}

	order.UpdatedAt = time.Now()

	if err := s.orderRepo.Update(ctx, order); err != nil {
		return nil, fmt.Errorf("failed to update order: %w", err)
	}

	return &paymentdto.CreatePaymentIntentResponse{
		ClientSecret:    pi.ClientSecret,
		PaymentIntentID: pi.ID,
		Amount:          pi.Amount,
		Currency:        string(pi.Currency),
	}, nil
}
