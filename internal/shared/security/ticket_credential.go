package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const ticketCredentialVersion = "OSMI1"

var (
	ErrInvalidTicketCredential       = errors.New("invalid ticket credential")
	ErrUnsupportedCredentialVersion  = errors.New("unsupported ticket credential version")
	ErrUnknownTicketCredentialKey    = errors.New("unknown ticket credential key")
	ErrInvalidTicketCredentialConfig = errors.New("invalid ticket credential configuration")
)

type TicketCredentialService struct {
	activeKeyID string
	signingKey  []byte
}

func NewTicketCredentialService(
	activeKeyID string,
	signingKey []byte,
) (*TicketCredentialService, error) {
	activeKeyID = strings.TrimSpace(activeKeyID)

	if activeKeyID == "" {
		return nil, fmt.Errorf(
			"%w: active key id is required",
			ErrInvalidTicketCredentialConfig,
		)
	}

	if strings.Contains(activeKeyID, ".") {
		return nil, fmt.Errorf(
			"%w: active key id cannot contain dots",
			ErrInvalidTicketCredentialConfig,
		)
	}

	if len(signingKey) < 32 {
		return nil, fmt.Errorf(
			"%w: signing key must contain at least 32 bytes",
			ErrInvalidTicketCredentialConfig,
		)
	}

	keyCopy := make([]byte, len(signingKey))
	copy(keyCopy, signingKey)

	return &TicketCredentialService{
		activeKeyID: activeKeyID,
		signingKey:  keyCopy,
	}, nil
}

func (s *TicketCredentialService) Sign(ticketPublicID string) (string, error) {
	ticketID, err := uuid.Parse(strings.TrimSpace(ticketPublicID))
	if err != nil {
		return "", fmt.Errorf(
			"%w: invalid ticket public id",
			ErrInvalidTicketCredential,
		)
	}

	payload := s.canonicalPayload(
		ticketCredentialVersion,
		s.activeKeyID,
		ticketID.String(),
	)

	signature := s.sign(payload)

	return payload + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *TicketCredentialService) Verify(
	credential string,
) (string, error) {
	parts := strings.Split(strings.TrimSpace(credential), ".")

	if len(parts) != 4 {
		return "", ErrInvalidTicketCredential
	}

	version := parts[0]
	keyID := parts[1]
	rawTicketID := parts[2]
	rawSignature := parts[3]

	if version != ticketCredentialVersion {
		return "", ErrUnsupportedCredentialVersion
	}

	if keyID != s.activeKeyID {
		return "", ErrUnknownTicketCredentialKey
	}

	ticketID, err := uuid.Parse(rawTicketID)
	if err != nil {
		return "", ErrInvalidTicketCredential
	}

	if ticketID.String() != rawTicketID {
		return "", ErrInvalidTicketCredential
	}

	providedSignature, err := base64.RawURLEncoding.DecodeString(
		rawSignature,
	)
	if err != nil {
		return "", ErrInvalidTicketCredential
	}

	// HMAC-SHA256 siempre produce exactamente 32 bytes.
	if len(providedSignature) != sha256.Size {
		return "", ErrInvalidTicketCredential
	}

	// Exigimos una única representación Base64URL canónica.
	// Esto evita aceptar strings diferentes que decodifiquen
	// al mismo conjunto de bytes por bits residuales de Base64.
	canonicalSignature := base64.RawURLEncoding.EncodeToString(
		providedSignature,
	)
	if canonicalSignature != rawSignature {
		return "", ErrInvalidTicketCredential
	}

	payload := s.canonicalPayload(
		version,
		keyID,
		ticketID.String(),
	)

	expectedSignature := s.sign(payload)

	if !hmac.Equal(expectedSignature, providedSignature) {
		return "", ErrInvalidTicketCredential
	}

	return ticketID.String(), nil
}

func (s *TicketCredentialService) canonicalPayload(
	version string,
	keyID string,
	ticketPublicID string,
) string {
	return version + "." + keyID + "." + ticketPublicID
}

func (s *TicketCredentialService) sign(payload string) []byte {
	mac := hmac.New(sha256.New, s.signingKey)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
