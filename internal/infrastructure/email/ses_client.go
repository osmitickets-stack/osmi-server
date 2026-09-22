package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/gomail.v2"
)

type SESClient struct {
	host     string
	port     int
	username string
	password string
	from     string
}

type TicketEmailItem struct {
	TicketCode     string
	TicketTypeName string
	EventName      string
	EventDate      string
	EventLocation  string
	QRBase64       string
}

type ticketTemplateItem struct {
	TicketCode     string
	TicketTypeName string
	EventName      string
	EventDate      string
	EventLocation  string
	QRCID          string
}

type ticketEmailTemplateData struct {
	CustomerName string
	OrderID      string
	Tickets      []ticketTemplateItem
}

func NewSESClient() *SESClient {
	port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	if port == 0 {
		port = 587
	}

	return &SESClient{
		host:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		port:     port,
		username: strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		password: os.Getenv("SMTP_PASSWORD"),
		from:     strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
}

func (s *SESClient) validateConfig() error {
	var missing []string

	if s.host == "" {
		missing = append(missing, "SMTP_HOST")
	}
	if s.username == "" {
		missing = append(missing, "SMTP_USERNAME")
	}
	if s.password == "" {
		missing = append(missing, "SMTP_PASSWORD")
	}
	if s.from == "" {
		missing = append(missing, "SMTP_FROM")
	}
	if s.port <= 0 {
		missing = append(missing, "SMTP_PORT")
	}

	if len(missing) > 0 {
		return fmt.Errorf(
			"SMTP configuration incomplete: missing %s",
			strings.Join(missing, ", "),
		)
	}

	return nil
}

func (s *SESClient) SendOrderTicketsEmail(
	toEmail string,
	customerName string,
	orderID string,
	tickets []TicketEmailItem,
) error {
	if err := s.validateConfig(); err != nil {
		return err
	}

	toEmail = strings.TrimSpace(toEmail)
	if toEmail == "" {
		return fmt.Errorf("recipient email is required")
	}

	if strings.TrimSpace(customerName) == "" {
		customerName = "Cliente osmi"
	}

	if strings.TrimSpace(orderID) == "" {
		return fmt.Errorf("order ID is required")
	}

	if len(tickets) == 0 {
		return fmt.Errorf("cannot send ticket email without tickets")
	}

	templateItems := make([]ticketTemplateItem, 0, len(tickets))

	m := gomail.NewMessage()
	m.SetHeader("From", fmt.Sprintf("osmi <%s>", s.from))
	m.SetHeader("To", toEmail)
	m.SetHeader("Subject", "Tus boletos están listos - osmi")

	for i, ticket := range tickets {
		if strings.TrimSpace(ticket.TicketCode) == "" {
			return fmt.Errorf("ticket %d has empty code", i+1)
		}

		if strings.TrimSpace(ticket.QRBase64) == "" {
			return fmt.Errorf(
				"ticket %s has empty QR data",
				ticket.TicketCode,
			)
		}

		qrPNG, err := base64.StdEncoding.DecodeString(ticket.QRBase64)
		if err != nil {
			return fmt.Errorf(
				"failed to decode QR for ticket %s: %w",
				ticket.TicketCode,
				err,
			)
		}

		cid := fmt.Sprintf("ticket-qr-%d", i+1)

		pngData := append([]byte(nil), qrPNG...)

		m.Embed(
			cid+".png",
			gomail.SetHeader(map[string][]string{
				"Content-ID": {
					fmt.Sprintf("<%s>", cid),
				},
			}),
			gomail.SetCopyFunc(func(w io.Writer) error {
				_, err := io.Copy(w, bytes.NewReader(pngData))
				return err
			}),
		)

		templateItems = append(
			templateItems,
			ticketTemplateItem{
				TicketCode:     ticket.TicketCode,
				TicketTypeName: ticket.TicketTypeName,
				EventName:      ticket.EventName,
				EventDate:      ticket.EventDate,
				EventLocation:  ticket.EventLocation,
				QRCID:          cid,
			},
		)
	}

	data := ticketEmailTemplateData{
		CustomerName: customerName,
		OrderID:      orderID,
		Tickets:      templateItems,
	}

	tmpl, err := template.New("ticket-email").Parse(ticketEmailHTML)
	if err != nil {
		return fmt.Errorf("failed to parse email template: %w", err)
	}

	var body bytes.Buffer

	if err := tmpl.Execute(&body, data); err != nil {
		return fmt.Errorf("failed to render email template: %w", err)
	}

	m.SetBody("text/html", body.String())

	dialer := gomail.NewDialer(
		s.host,
		s.port,
		s.username,
		s.password,
	)

	if err := dialer.DialAndSend(m); err != nil {
		return fmt.Errorf("failed to send ticket email: %w", err)
	}

	return nil
}

const ticketEmailHTML = `
<!DOCTYPE html>
<html lang="es">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Tus boletos - osmi</title>
</head>

<body style="margin:0;padding:0;background-color:#05010f;font-family:Arial,Helvetica,sans-serif;">
	<table width="100%" cellpadding="0" cellspacing="0" style="background-color:#05010f;padding:40px 20px;">
		<tr>
			<td align="center">

				<table width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;">

					<tr>
						<td style="padding-bottom:30px;text-align:center;">
							<span style="font-size:36px;font-weight:900;color:#ff2bd6;">osmi</span>
							<p style="color:#6f6a7d;font-size:12px;margin-top:4px;text-transform:uppercase;letter-spacing:3px;">
								momentos inolvidables
							</p>
						</td>
					</tr>

					<tr>
						<td style="background:#0c0816;border:1px solid #221a30;border-radius:24px;padding:40px;">

							<span style="display:inline-block;background:#0f2819;border:1px solid #1f5132;color:#22c55e;font-size:11px;font-weight:700;padding:8px 16px;border-radius:99px;text-transform:uppercase;letter-spacing:2px;">
								Compra Confirmada
							</span>

							<h1 style="color:#f0edf6;font-size:28px;font-weight:900;margin:24px 0 8px 0;">
								Gracias, {{.CustomerName}}!
							</h1>

							<p style="color:#a09ab5;font-size:16px;margin:0 0 8px 0;line-height:1.5;">
								Tus boletos están listos.
							</p>

							<p style="color:#6f6a7d;font-size:12px;margin:0 0 32px 0;">
								Orden: {{.OrderID}}
							</p>

							{{range .Tickets}}

							<table width="100%" cellpadding="0" cellspacing="0" style="background:#110c1d;border:1px solid #241a36;border-radius:16px;padding:24px;margin-bottom:24px;">
								<tr>
									<td style="padding-bottom:8px;color:#f0edf6;font-size:18px;font-weight:700;">
										{{.TicketTypeName}}
									</td>
								</tr>

								<tr>
									<td style="padding-bottom:16px;color:#ff2bd6;font-size:15px;font-weight:700;">
										{{.EventName}}
									</td>
								</tr>

								{{if .EventDate}}
								<tr>
									<td style="padding-bottom:8px;color:#a09ab5;font-size:13px;">
										{{.EventDate}}
									</td>
								</tr>
								{{end}}

								{{if .EventLocation}}
								<tr>
									<td style="padding-bottom:16px;color:#a09ab5;font-size:13px;">
										{{.EventLocation}}
									</td>
								</tr>
								{{end}}

								<tr>
									<td align="center" style="padding:20px 0 16px 0;">
										<img
											src="cid:{{.QRCID}}"
											alt="Código QR"
											width="180"
											height="180"
											style="display:block;background:white;padding:12px;border-radius:12px;"
										/>
									</td>
								</tr>

								<tr>
									<td align="center">
										<span style="display:inline-block;background:#251025;border:1px solid #55204f;color:#ff2bd6;font-size:16px;font-weight:900;padding:12px 20px;border-radius:12px;letter-spacing:2px;">
											{{.TicketCode}}
										</span>
									</td>
								</tr>
							</table>

							{{end}}

							<p style="color:#6f6a7d;font-size:12px;text-align:center;margin:24px 0 0 0;">
								Cada código QR corresponde a un boleto individual.
							</p>

							<p style="color:#6f6a7d;font-size:11px;text-align:center;margin:8px 0 0 0;">
								osmi &copy; 2026 · momentos inolvidables
							</p>

						</td>
					</tr>

				</table>

			</td>
		</tr>
	</table>
</body>
</html>
`
