package security

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestTicketCredentialService(t *testing.T) *TicketCredentialService {
	t.Helper()

	service, err := NewTicketCredentialService(
		"k1",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("create credential service: %v", err)
	}

	return service
}

func TestTicketCredentialRoundTrip(t *testing.T) {
	service := newTestTicketCredentialService(t)

	ticketID := uuid.New().String()

	credential, err := service.Sign(ticketID)
	if err != nil {
		t.Fatalf("sign credential: %v", err)
	}

	gotTicketID, err := service.Verify(credential)
	if err != nil {
		t.Fatalf("verify credential: %v", err)
	}

	if gotTicketID != ticketID {
		t.Fatalf(
			"expected ticket id %q, got %q",
			ticketID,
			gotTicketID,
		)
	}

	if !strings.HasPrefix(credential, "OSMI1.k1.") {
		t.Fatalf(
			"unexpected credential format: %q",
			credential,
		)
	}
}

func TestTicketCredentialRejectsTamperedTicketID(t *testing.T) {
	service := newTestTicketCredentialService(t)

	credential, err := service.Sign(uuid.New().String())
	if err != nil {
		t.Fatalf("sign credential: %v", err)
	}

	parts := strings.Split(credential, ".")
	parts[2] = uuid.New().String()

	_, err = service.Verify(strings.Join(parts, "."))
	if !errors.Is(err, ErrInvalidTicketCredential) {
		t.Fatalf(
			"expected invalid credential, got %v",
			err,
		)
	}
}

func TestTicketCredentialRejectsTamperedSignature(t *testing.T) {
	service := newTestTicketCredentialService(t)

	credential, err := service.Sign(uuid.New().String())
	if err != nil {
		t.Fatalf("sign credential: %v", err)
	}

	parts := strings.Split(credential, ".")
	if len(parts) != 4 {
		t.Fatalf(
			"unexpected credential format: %q",
			credential,
		)
	}

	signature, err := base64.RawURLEncoding.DecodeString(
		parts[3],
	)
	if err != nil {
		t.Fatalf(
			"decode signature: %v",
			err,
		)
	}

	if len(signature) == 0 {
		t.Fatal("signature must not be empty")
	}

	// Alteramos realmente los bytes criptográficos,
	// no sólo la representación Base64.
	signature[0] ^= 0x01

	parts[3] = base64.RawURLEncoding.EncodeToString(
		signature,
	)

	_, err = service.Verify(
		strings.Join(parts, "."),
	)
	if !errors.Is(err, ErrInvalidTicketCredential) {
		t.Fatalf(
			"expected invalid credential, got %v",
			err,
		)
	}
}

func TestTicketCredentialRejectsUnknownVersion(t *testing.T) {
	service := newTestTicketCredentialService(t)

	credential, err := service.Sign(uuid.New().String())
	if err != nil {
		t.Fatalf("sign credential: %v", err)
	}

	credential = strings.Replace(
		credential,
		"OSMI1.",
		"OSMI2.",
		1,
	)

	_, err = service.Verify(credential)
	if !errors.Is(err, ErrUnsupportedCredentialVersion) {
		t.Fatalf(
			"expected unsupported version, got %v",
			err,
		)
	}
}

func TestTicketCredentialRejectsUnknownKeyID(t *testing.T) {
	service := newTestTicketCredentialService(t)

	credential, err := service.Sign(uuid.New().String())
	if err != nil {
		t.Fatalf("sign credential: %v", err)
	}

	credential = strings.Replace(
		credential,
		".k1.",
		".k2.",
		1,
	)

	_, err = service.Verify(credential)
	if !errors.Is(err, ErrUnknownTicketCredentialKey) {
		t.Fatalf(
			"expected unknown key id, got %v",
			err,
		)
	}
}

func TestTicketCredentialRejectsMalformedCredential(t *testing.T) {
	service := newTestTicketCredentialService(t)

	cases := []string{
		"",
		"hello",
		"OSMI1",
		"OSMI1.k1",
		"OSMI1.k1.not-a-uuid.signature",
		"OSMI1.k1.a.b.c",
	}

	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			if _, err := service.Verify(tc); err == nil {
				t.Fatalf(
					"expected credential %q to fail",
					tc,
				)
			}
		})
	}
}

func TestTicketCredentialRejectsShortKey(t *testing.T) {
	_, err := NewTicketCredentialService(
		"k1",
		[]byte("short"),
	)

	if !errors.Is(
		err,
		ErrInvalidTicketCredentialConfig,
	) {
		t.Fatalf(
			"expected configuration error, got %v",
			err,
		)
	}
}
