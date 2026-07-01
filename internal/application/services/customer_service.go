// internal/application/services/customer_service.go
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	commondto "github.com/osmitickets-stack/osmi-server/internal/api/dto/common"
	customerdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/customer"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
)

// CreateCustomerRequest - DTO interno para creación de clientes
type CreateCustomerRequest struct {
	UserID *int64

	FullName string
	Email    string
	Phone    *string

	CompanyName *string

	AddressLine1 *string
	AddressLine2 *string
	City         *string
	State        *string
	PostalCode   *string
	Country      *string

	TaxID     *string
	TaxIDType *string
	TaxName   *string

	RequiresInvoice bool

	CommunicationPreferences map[string]any

	//CustomerType string
	//Source       string
}

// UpdateCustomerRequest - DTO para actualizar cliente
type UpdateCustomerRequest struct {
	FullName *string

	Phone *string

	CompanyName *string

	AddressLine1 *string
	AddressLine2 *string
	City         *string
	State        *string
	PostalCode   *string
	Country      *string

	TaxID     *string
	TaxIDType *string
	TaxName   *string

	RequiresInvoice *bool

	CommunicationPreferences map[string]any

	IsVIP *bool

	LastPurchaseAt *time.Time
}

type CustomerService struct {
	customerRepo repository.CustomerRepository
}

func NewCustomerService(customerRepo repository.CustomerRepository) *CustomerService {
	return &CustomerService{
		customerRepo: customerRepo,
	}
}

// ============================================================================
// MÉTODOS EXISTENTES
// ============================================================================

// CreateCustomer crea un nuevo cliente
func (s *CustomerService) CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*entities.Customer, error) {
	// Validar request
	if req.FullName == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Email == "" {
		return nil, fmt.Errorf("email is required")
	}

	now := time.Now()
	customer := &entities.Customer{
		PublicID: uuid.New().String(),

		UserID: req.UserID,

		FullName: req.FullName,
		Email:    req.Email,
		Phone:    req.Phone,

		CompanyName: req.CompanyName,

		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
		City:         req.City,
		State:        req.State,
		PostalCode:   req.PostalCode,
		Country:      req.Country,

		TaxID:     req.TaxID,
		TaxIDType: req.TaxIDType,
		TaxName:   req.TaxName,

		RequiresInvoice: req.RequiresInvoice,

		CommunicationPreferences: req.CommunicationPreferences,

		TotalSpent:    0,
		TotalOrders:   0,
		TotalTickets:  0,
		AvgOrderValue: 0,

		IsActive: true,
		IsVIP:    false,

		CustomerSegment: "new",

		LifetimeValue: 0,

		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.customerRepo.Create(ctx, customer); err != nil {
		return nil, fmt.Errorf("failed to create customer: %w", err)
	}

	return customer, nil
}

// GetCustomer obtiene un cliente por su PublicID
func (s *CustomerService) GetCustomer(ctx context.Context, publicID string) (*entities.Customer, error) {
	if publicID == "" {
		return nil, fmt.Errorf("customer ID is required")
	}

	customer, err := s.customerRepo.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer: %w", err)
	}

	return customer, nil
}

// ============================================================================
// NUEVOS MÉTODOS (IMPLEMENTADOS)
// ============================================================================

// UpdateCustomer actualiza la información de un cliente
func (s *CustomerService) UpdateCustomer(ctx context.Context, publicID string, req *UpdateCustomerRequest) (*entities.Customer, error) {
	// Obtener el cliente existente
	customer, err := s.customerRepo.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, fmt.Errorf("customer not found: %w", err)
	}

	// Actualizar campos si se proporcionan
	if req.FullName != nil {
		customer.FullName = *req.FullName
	}

	if req.Phone != nil {
		customer.Phone = req.Phone
	}

	if req.CompanyName != nil {
		customer.CompanyName = req.CompanyName
	}

	if req.AddressLine1 != nil {
		customer.AddressLine1 = req.AddressLine1
	}

	if req.AddressLine2 != nil {
		customer.AddressLine2 = req.AddressLine2
	}

	if req.City != nil {
		customer.City = req.City
	}

	if req.State != nil {
		customer.State = req.State
	}

	if req.PostalCode != nil {
		customer.PostalCode = req.PostalCode
	}

	if req.Country != nil {
		customer.Country = req.Country
	}

	if req.TaxID != nil {
		customer.TaxID = req.TaxID
	}

	if req.TaxIDType != nil {
		customer.TaxIDType = req.TaxIDType
	}

	if req.TaxName != nil {
		customer.TaxName = req.TaxName
	}

	if req.RequiresInvoice != nil {
		customer.RequiresInvoice = *req.RequiresInvoice
	}

	if req.CommunicationPreferences != nil {
		customer.CommunicationPreferences = req.CommunicationPreferences
	}

	if req.IsVIP != nil {
		customer.IsVIP = *req.IsVIP
	}

	if req.LastPurchaseAt != nil {
		customer.LastPurchaseAt = req.LastPurchaseAt
	}

	customer.UpdatedAt = time.Now()

	if err := s.customerRepo.Update(ctx, customer); err != nil {
		return nil, fmt.Errorf("failed to update customer: %w", err)
	}

	return customer, nil
}

// ListCustomers lista clientes con filtros y paginación
func (s *CustomerService) ListCustomers(ctx context.Context, filter *customerdto.CustomerFilter, pagination commondto.Pagination) ([]*entities.Customer, int64, error) {
	// Convertir filtro DTO a filtro del repositorio
	repoFilter := &repository.CustomerFilter{
		Limit:  pagination.PageSize,
		Offset: (pagination.Page - 1) * pagination.PageSize,
	}

	if filter != nil {
		if filter.IsActive != nil {
			repoFilter.IsActive = filter.IsActive
		}
		if filter.IsVIP != nil {
			repoFilter.IsVIP = filter.IsVIP
		}
		if filter.Country != "" {
			repoFilter.Country = &filter.Country
		}
		if filter.CustomerSegment != "" {
			repoFilter.CustomerSegment = &filter.CustomerSegment
		}
		if filter.Search != "" {
			repoFilter.SearchTerm = &filter.Search
		}
		if filter.DateFrom != "" {
			// Convertir string a time.Time si es necesario
		}
		if filter.DateTo != "" {
			// Convertir string a time.Time si es necesario
		}
	}

	return s.customerRepo.Find(ctx, repoFilter)
}

// GetCustomerStats obtiene estadísticas globales de clientes
func (s *CustomerService) GetCustomerStats(ctx context.Context) (*customerdto.CustomerStatsResponse, error) {
	// Usar el método del repositorio
	stats, err := s.customerRepo.GetStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer stats: %w", err)
	}

	// Convertir a DTO
	return &customerdto.CustomerStatsResponse{
		TotalCustomers:         stats.TotalCustomers,
		ActiveCustomers:        stats.ActiveCustomers,
		VIPCustomers:           stats.VIPCustomers,
		NewCustomersLast30Days: stats.NewCustomersLast30Days,
		TotalRevenue:           stats.TotalRevenue,
		AvgLifetimeValue:       stats.AvgLifetimeValue,
		TopCountries:           convertCountryStatsToDTO(stats.TopCountries),
	}, nil
}

// convertCountryStatsToDTO convierte []repository.CountryStat a []customerdto.CountryStats
func convertCountryStatsToDTO(stats []repository.CountryStat) []customerdto.CountryStats {
	result := make([]customerdto.CountryStats, len(stats))
	for i, stat := range stats {
		result[i] = customerdto.CountryStats{
			Country: stat.Country,
			Count:   stat.Count,
			Revenue: stat.Revenue,
		}
	}
	return result
}
