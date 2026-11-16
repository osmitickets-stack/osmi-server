package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	commondto "github.com/osmitickets-stack/osmi-server/internal/api/dto/common"
	orderdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/order"
	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
)

func TestOrderCustomerOwnershipIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	accountEmail := fmt.Sprintf("registered-order-%d@example.invalid", suffix)
	guestEmail := fmt.Sprintf("guest-order-%d@example.invalid", suffix)
	var userID, customerID int64
	var userPublicID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth.users(email,password_hash,is_active)
		VALUES ($1,'test-only-hash',true) RETURNING id,public_uuid::text
	`, accountEmail).Scan(&userID, &userPublicID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanup, `DELETE FROM crm.customers WHERE id=$1`, customerID)
		_, _ = pool.Exec(cleanup, `DELETE FROM auth.users WHERE id=$1`, userID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO crm.customers(user_id,full_name,email)
		VALUES ($1,'Registered Buyer',$2) RETURNING id
	`, userID, accountEmail).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	_, signer := newCheckInTestService(t)
	service := NewOrderService(
		postgres.NewOrderRepository(pool), postgres.NewCustomerRepository(pool),
		postgres.NewTicketTypeRepository(pool), postgres.NewTicketRepository(pool), signer,
	)
	create := func(email, suppliedCustomerID string, requestCtx context.Context) int64 {
		t.Helper()
		typeID := concurrencyTicketType(t, pool, 1)
		var typePublicID string
		if err := pool.QueryRow(ctx, `SELECT public_uuid::text FROM ticketing.ticket_types WHERE id=$1`, typeID).Scan(&typePublicID); err != nil {
			t.Fatal(err)
		}
		order, _, err := service.CreateOrder(requestCtx, &orderdto.CreateOrderRequest{
			CustomerID: suppliedCustomerID, CustomerEmail: email, CustomerName: "Buyer",
			Items: []orderdto.CreateOrderItemRequest{{TicketTypeID: typePublicID, Quantity: 1}},
		})
		if err != nil {
			t.Fatalf("create isolated order: %v", err)
		}
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.tickets WHERE order_id=$1`, order.ID)
			_, _ = pool.Exec(cleanup, `DELETE FROM billing.order_items WHERE order_id=$1`, order.ID)
			_, _ = pool.Exec(cleanup, `DELETE FROM billing.orders WHERE id=$1`, order.ID)
		})
		return order.ID
	}
	guestOrderID := create(guestEmail, "spoofed-customer-id", ctx)
	var guestOrderUnlinked, guestTicketUnlinked bool
	if err := pool.QueryRow(ctx, `
		SELECT o.customer_id IS NULL, t.customer_id IS NULL
		FROM billing.orders o JOIN ticketing.tickets t ON t.order_id=o.id WHERE o.id=$1
	`, guestOrderID).Scan(&guestOrderUnlinked, &guestTicketUnlinked); err != nil || !guestOrderUnlinked || !guestTicketUnlinked {
		t.Fatalf("guest order linked to a customer: order=%v ticket=%v err=%v", guestOrderUnlinked, guestTicketUnlinked, err)
	}
	accountCtx := osmicontext.WithUserID(ctx, userPublicID)
	registeredOrderID := create(accountEmail, "spoofed-customer-id", accountCtx)
	var orderCustomerID, ticketCustomerID int64
	if err := pool.QueryRow(ctx, `
		SELECT o.customer_id, t.customer_id
		FROM billing.orders o JOIN ticketing.tickets t ON t.order_id=o.id WHERE o.id=$1
	`, registeredOrderID).Scan(&orderCustomerID, &ticketCustomerID); err != nil || orderCustomerID != customerID || ticketCustomerID != customerID {
		t.Fatalf("registered order owner mismatch: order=%d ticket=%d err=%v", orderCustomerID, ticketCustomerID, err)
	}
	ticketService := NewTicketService(postgres.NewTicketRepository(pool), nil, nil, nil, nil, nil)
	ownerID, err := ticketService.GetCustomerIDByUserID(ctx, userPublicID)
	if err != nil || ownerID != customerID {
		t.Fatalf("registered ticket lookup owner mismatch: owner=%d err=%v", ownerID, err)
	}
	ownedTickets, total, err := ticketService.ListTickets(ctx,
		&ticketdto.TicketFilter{CustomerID: &ownerID}, commondto.Pagination{Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(ownedTickets) != 1 || ownedTickets[0].OrderID == nil || *ownedTickets[0].OrderID != registeredOrderID {
		t.Fatalf("account ticket list includes another purchase: total=%d count=%d err=%v", total, len(ownedTickets), err)
	}
	if _, _, err := service.CreateOrder(accountCtx, &orderdto.CreateOrderRequest{CustomerEmail: guestEmail}); err == nil {
		t.Fatal("authenticated order accepted a different email")
	}
}
