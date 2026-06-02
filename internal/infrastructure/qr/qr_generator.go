package qr

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"

	"github.com/skip2/go-qrcode"
)

func GenerateQRBase64(data string) (string, error) {
	qr, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("failed to generate QR: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, qr.Image(256)); err != nil {
		return "", fmt.Errorf("failed to encode QR: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
