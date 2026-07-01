// internal/api/grpc/adapter.go
package grpc

import (
	"github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/api/dto/customer"
	"github.com/osmitickets-stack/osmi-server/internal/api/helpers"
	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ProtoToCreateCustomerRequest convierte protobuf a DTO
func ProtoToCreateCustomerRequest(req *pb.CreateCustomerRequest) *customer.CreateCustomerRequest {
	return &customer.CreateCustomerRequest{
		FullName:     req.Name,
		Email:        req.Email,
		Phone:        helpers.StringPtr(req.Phone),
		CompanyName:  helpers.StringPtr(req.CompanyName),
		AddressLine1: helpers.StringPtr(req.AddressLine1),
		AddressLine2: helpers.StringPtr(req.AddressLine2),
		City:         helpers.StringPtr(req.City),
		State:        helpers.StringPtr(req.State),
		PostalCode:   helpers.StringPtr(req.PostalCode),
		Country:      helpers.StringPtr(req.Country),
		TaxID:        helpers.StringPtr(req.TaxId),

		RequiresInvoice: req.RequiresInvoice,
	}
}

// CustomerToProto convierte entidad Customer a protobuf
func CustomerToProto(customer *entities.Customer) *pb.CustomerResponse {
	return &pb.CustomerResponse{
		Id:              int32(customer.ID),
		PublicId:        customer.PublicID,
		Name:            customer.FullName,
		Email:           customer.Email,
		Phone:           helpers.SafeStringPtr(customer.Phone),
		CompanyName:     helpers.SafeStringPtr(customer.CompanyName),
		AddressLine1:    helpers.SafeStringPtr(customer.AddressLine1),
		AddressLine2:    helpers.SafeStringPtr(customer.AddressLine2),
		City:            helpers.SafeStringPtr(customer.City),
		State:           helpers.SafeStringPtr(customer.State),
		PostalCode:      helpers.SafeStringPtr(customer.PostalCode),
		Country:         helpers.SafeStringPtr(customer.Country),
		TaxId:           helpers.SafeStringPtr(customer.TaxID),
		TaxName:         helpers.SafeStringPtr(customer.TaxName),
		TaxIdType:       pb.TaxIdType_TAX_ID_TYPE_UNSPECIFIED,
		RequiresInvoice: customer.RequiresInvoice,
		TotalSpent:      customer.TotalSpent,
		TotalOrders:     int32(customer.TotalOrders),
		TotalTickets:    int32(customer.TotalTickets),
		AvgOrderValue:   customer.AvgOrderValue,
		IsActive:        customer.IsActive,
		IsVip:           customer.IsVIP,
		CustomerSegment: pb.CustomerSegment_CUSTOMER_SEGMENT_UNSPECIFIED,
		LifetimeValue:   customer.LifetimeValue,
		CreatedAt:       timestamppb.New(customer.CreatedAt),
		UpdatedAt:       timestamppb.New(customer.UpdatedAt),
	}
}
