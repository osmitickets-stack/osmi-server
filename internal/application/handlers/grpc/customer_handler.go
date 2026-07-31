// internal/application/handlers/grpc/customer_handler.go
package grpc

import (
	"context"

	osmi "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	commondto "github.com/osmitickets-stack/osmi-server/internal/api/dto/common"
	customerdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/customer"
	"github.com/osmitickets-stack/osmi-server/internal/api/helpers"
	"github.com/osmitickets-stack/osmi-server/internal/application/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CustomerHandler struct {
	osmi.UnimplementedOsmiServiceServer
	customerService *services.CustomerService
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func NewCustomerHandler(customerService *services.CustomerService) *CustomerHandler {
	return &CustomerHandler{
		customerService: customerService,
	}
}

// ============================================================================
// MÉTODOS IMPLEMENTADOS
// ============================================================================

// CreateCustomer maneja la creación de un nuevo cliente
func (h *CustomerHandler) CreateCustomer(ctx context.Context, req *osmi.CreateCustomerRequest) (*osmi.CustomerResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	var taxIDType *string
	if req.TaxIdType != osmi.TaxIdType_TAX_ID_TYPE_UNSPECIFIED {
		v := req.TaxIdType.String()
		taxIDType = &v
	}

	createReq := &services.CreateCustomerRequest{
		UserID:          nil,
		FullName:        req.Name,
		Email:           req.Email,
		Phone:           stringPtr(req.Phone),
		CompanyName:     stringPtr(req.CompanyName),
		AddressLine1:    stringPtr(req.AddressLine1),
		AddressLine2:    stringPtr(req.AddressLine2),
		City:            stringPtr(req.City),
		State:           stringPtr(req.State),
		PostalCode:      stringPtr(req.PostalCode),
		Country:         stringPtr(req.Country),
		TaxID:           stringPtr(req.TaxId),
		TaxName:         stringPtr(req.TaxName),
		TaxIDType:       taxIDType,
		RequiresInvoice: req.RequiresInvoice,
	}

	customer, err := h.customerService.CreateCustomer(ctx, createReq)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &osmi.CustomerResponse{
		Id:           int32(customer.ID),
		PublicId:     customer.PublicID,
		Name:         customer.FullName,
		Email:        customer.Email,
		Phone:        helpers.SafeStringPtr(customer.Phone),
		CustomerType: req.CustomerType,
		IsVip:        customer.IsVIP,
		TotalSpent:   customer.TotalSpent,
		TotalOrders:  int32(customer.TotalOrders),
		CreatedAt:    timestamppb.New(customer.CreatedAt),
		UpdatedAt:    timestamppb.New(customer.UpdatedAt),
	}, nil
}

// GetCustomer obtiene un cliente por su ID público
func (h *CustomerHandler) GetCustomer(ctx context.Context, req *osmi.GetCustomerRequest) (*osmi.CustomerResponse, error) {
	if req.PublicId == "" {
		return nil, status.Error(codes.InvalidArgument, "public_id cannot be empty")
	}

	customer, err := h.customerService.GetCustomer(ctx, req.PublicId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &osmi.CustomerResponse{
		Id:           int32(customer.ID),
		PublicId:     customer.PublicID,
		Name:         customer.FullName,
		Email:        customer.Email,
		Phone:        helpers.SafeStringPtr(customer.Phone),
		CustomerType: osmi.CustomerType_CUSTOMER_TYPE_GUEST,
		IsVip:        customer.IsVIP,
		TotalSpent:   customer.TotalSpent,
		TotalOrders:  int32(customer.TotalOrders),
		CreatedAt:    timestamppb.New(customer.CreatedAt),
		UpdatedAt:    timestamppb.New(customer.UpdatedAt),
	}, nil
}

// UpdateCustomer actualiza la información de un cliente
func (h *CustomerHandler) UpdateCustomer(ctx context.Context, req *osmi.UpdateCustomerRequest) (*osmi.CustomerResponse, error) {
	if req.PublicId == "" {
		return nil, status.Error(codes.InvalidArgument, "customer public_id is required")
	}

	updateReq := &services.UpdateCustomerRequest{
		FullName:    req.Name,
		Phone:       req.Phone,
		CompanyName: req.CompanyName,
		IsVIP:       req.IsVip,
	}

	customer, err := h.customerService.UpdateCustomer(ctx, req.PublicId, updateReq)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &osmi.CustomerResponse{
		Id:           int32(customer.ID),
		PublicId:     customer.PublicID,
		Name:         customer.FullName,
		Email:        customer.Email,
		Phone:        helpers.SafeStringPtr(customer.Phone),
		CustomerType: osmi.CustomerType_CUSTOMER_TYPE_GUEST,
		IsVip:        customer.IsVIP,
		TotalSpent:   customer.TotalSpent,
		TotalOrders:  int32(customer.TotalOrders),
		CreatedAt:    timestamppb.New(customer.CreatedAt),
		UpdatedAt:    timestamppb.New(customer.UpdatedAt),
	}, nil
}

// ListCustomers lista clientes con filtros y paginación
func (h *CustomerHandler) ListCustomers(ctx context.Context, req *osmi.ListCustomersRequest) (*osmi.CustomerListResponse, error) {
	// ============================================================
	// CRÍTICO: req.Filter puede ser nil
	// ============================================================

	// Crear un filtro vacío por defecto
	filter := &customerdto.CustomerFilter{}

	// Si req.Filter no es nil, usarlo
	if req.Filter != nil {
		filter.Search = req.Filter.Search
		filter.Email = req.Filter.Email
		filter.Phone = req.Filter.Phone
		filter.TaxID = req.Filter.TaxId
		filter.PublicID = req.Filter.PublicId
		filter.CompanyName = req.Filter.CompanyName
		filter.Country = req.Filter.Country
		filter.CustomerSegment = req.Filter.CustomerSegment
		filter.DateFrom = req.Filter.DateFrom
		filter.DateTo = req.Filter.DateTo

		// Usar BoolValue para IsActive
		if req.Filter.IsActive != nil {
			filter.IsActive = &req.Filter.IsActive.Value
		}

		// Usar BoolValue para IsVIP
		if req.Filter.IsVip != nil {
			filter.IsVIP = &req.Filter.IsVip.Value
		}
	}

	// Paginación
	pagination := commondto.Pagination{
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	if pagination.Page <= 0 {
		pagination.Page = 1
	}
	if pagination.PageSize <= 0 {
		pagination.PageSize = 20
	}

	// Llamar al servicio
	customers, total, err := h.customerService.ListCustomers(ctx, filter, pagination)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Convertir a respuesta
	pbCustomers := make([]*osmi.CustomerResponse, len(customers))
	for i, customer := range customers {
		pbCustomers[i] = &osmi.CustomerResponse{
			Id:           int32(customer.ID),
			PublicId:     customer.PublicID,
			Name:         customer.FullName,
			Email:        customer.Email,
			Phone:        helpers.SafeStringPtr(customer.Phone),
			CustomerType: osmi.CustomerType_CUSTOMER_TYPE_GUEST,
			IsVip:        customer.IsVIP,
			TotalSpent:   customer.TotalSpent,
			TotalOrders:  int32(customer.TotalOrders),
			CreatedAt:    timestamppb.New(customer.CreatedAt),
			UpdatedAt:    timestamppb.New(customer.UpdatedAt),
		}
	}

	// Calcular total de páginas
	totalPages := int32(0)
	if pagination.PageSize > 0 {
		totalPages = int32((int(total) + pagination.PageSize - 1) / pagination.PageSize)
	}

	return &osmi.CustomerListResponse{
		Customers:  pbCustomers,
		TotalCount: int32(total),
		Page:       int32(pagination.Page),
		PageSize:   int32(pagination.PageSize),
		TotalPages: totalPages,
	}, nil
}

// GetCustomerStats obtiene estadísticas de clientes
func (h *CustomerHandler) GetCustomerStats(ctx context.Context, req *osmi.Empty) (*osmi.CustomerStatsResponse, error) {
	stats, err := h.customerService.GetCustomerStats(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	topCountries := make([]*osmi.CountryStats, len(stats.TopCountries))
	for i, country := range stats.TopCountries {
		topCountries[i] = &osmi.CountryStats{
			Country: country.Country,
			Count:   int64(country.Count),
			Revenue: country.Revenue,
		}
	}

	return &osmi.CustomerStatsResponse{
		TotalCustomers:          stats.TotalCustomers,
		ActiveCustomers:         stats.ActiveCustomers,
		VipCustomers:            stats.VIPCustomers,
		NewCustomersLast_30Days: stats.NewCustomersLast30Days,
		TotalRevenue:            stats.TotalRevenue,
		AvgLifetimeValue:        stats.AvgLifetimeValue,
		TopCountries:            topCountries,
	}, nil
}
