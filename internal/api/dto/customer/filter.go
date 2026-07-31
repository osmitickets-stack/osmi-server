// internal/api/dto/customer/filter.go
package customer

type CustomerFilter struct {
	Search          string `json:"search,omitempty"`
	Email           string `json:"email,omitempty"`
	Phone           string `json:"phone,omitempty"`
	TaxID           string `json:"tax_id,omitempty"`
	PublicID        string `json:"public_id,omitempty"`
	CompanyName     string `json:"company_name,omitempty"`
	Country         string `json:"country,omitempty"`
	CustomerSegment string `json:"customer_segment,omitempty"`
	IsActive        *bool  `json:"is_active,omitempty"`
	IsVIP           *bool  `json:"is_vip,omitempty"`
	DateFrom        string `json:"date_from,omitempty"`
	DateTo          string `json:"date_to,omitempty"`
}
