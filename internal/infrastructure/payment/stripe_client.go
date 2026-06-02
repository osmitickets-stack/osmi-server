package payment

import (
	"github.com/stripe/stripe-go/v83"
	"github.com/stripe/stripe-go/v83/paymentintent"
)

type StripeClient struct {
	secretKey string
}

func NewStripeClient(secretKey string) *StripeClient {
	stripe.Key = secretKey

	return &StripeClient{
		secretKey: secretKey,
	}
}

func (c *StripeClient) CreatePaymentIntent(
	amount int64,
	currency string,
	orderID string,
) (*stripe.PaymentIntent, error) {

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String(currency),

		CaptureMethod: stripe.String("automatic"),

		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),

		Metadata: map[string]string{
			"order_id": orderID,
		},
	}

	return paymentintent.New(params)
}

func (c *StripeClient) GetPaymentIntent(
	id string,
) (*stripe.PaymentIntent, error) {
	return paymentintent.Get(id, nil)
}
