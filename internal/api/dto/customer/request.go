package customer

import "time"

// ========================
// CREATE DTO
// ========================
type CreateCustomerRequest struct {
	UserID *int64 `json:"user_id,omitempty"`

	FullName string  `json:"full_name" validate:"required,max=255"`
	Email    string  `json:"email" validate:"required,email,max=320"`
	Phone    *string `json:"phone,omitempty" validate:"omitempty,phone"`

	CompanyName *string `json:"company_name,omitempty" validate:"omitempty,max=255"`

	AddressLine1 *string `json:"address_line1,omitempty" validate:"omitempty,max=255"`
	AddressLine2 *string `json:"address_line2,omitempty" validate:"omitempty,max=255"`
	City         *string `json:"city,omitempty" validate:"omitempty,max=100"`
	State        *string `json:"state,omitempty" validate:"omitempty,max=100"`
	PostalCode   *string `json:"postal_code,omitempty" validate:"omitempty,max=20"`
	Country      *string `json:"country,omitempty" validate:"omitempty,country_code"`

	TaxID     *string `json:"tax_id,omitempty" validate:"omitempty,max=50"`
	TaxIDType *string `json:"tax_id_type,omitempty" validate:"omitempty,oneof=rfc ein vat other"`
	TaxName   *string `json:"tax_name,omitempty" validate:"omitempty,max=255"`

	RequiresInvoice bool `json:"requires_invoice"`

	CommunicationPreferences map[string]any `json:"communication_preferences,omitempty"`

	CustomerType string
	Source       string
}

// ========================
// UPDATE DTO
// ========================
type UpdateCustomerRequest struct {
	FullName *string `json:"full_name,omitempty" validate:"omitempty,max=255"`

	Phone *string `json:"phone,omitempty" validate:"omitempty,phone"`

	CompanyName *string `json:"company_name,omitempty" validate:"omitempty,max=255"`

	AddressLine1 *string `json:"address_line1,omitempty" validate:"omitempty,max=255"`
	AddressLine2 *string `json:"address_line2,omitempty" validate:"omitempty,max=255"`
	City         *string `json:"city,omitempty" validate:"omitempty,max=100"`
	State        *string `json:"state,omitempty" validate:"omitempty,max=100"`
	PostalCode   *string `json:"postal_code,omitempty" validate:"omitempty,max=20"`
	Country      *string `json:"country,omitempty" validate:"omitempty,country_code"`

	TaxID     *string `json:"tax_id,omitempty" validate:"omitempty,max=50"`
	TaxIDType *string `json:"tax_id_type,omitempty" validate:"omitempty,oneof=rfc ein vat other"`

	RequiresInvoice *bool `json:"requires_invoice,omitempty"`

	TaxName *string `json:"tax_name,omitempty" validate:"omitempty,max=255"`

	CommunicationPreferences map[string]any `json:"communication_preferences,omitempty"`

	LastPurchaseAt *time.Time `json:"last_purchase_at,omitempty"`
}
