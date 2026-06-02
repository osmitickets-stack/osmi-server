package email

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/gomail.v2"
)

type SESClient struct {
	host     string
	port     int
	username string
	password string
	from     string
}

func NewSESClient() *SESClient {
	port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	if port == 0 {
		port = 587
	}

	return &SESClient{
		host:     os.Getenv("SMTP_HOST"),
		port:     port,
		username: os.Getenv("SMTP_USERNAME"),
		password: os.Getenv("SMTP_PASSWORD"),
		from:     os.Getenv("SMTP_FROM"),
	}
}

func (s *SESClient) SendTicketEmail(
	toEmail, customerName, eventName, eventDate, eventLocation, ticketCode, qrBase64 string,
) error {
	if s.host == "" {
		fmt.Println("⚠️ SMTP not configured, skipping email")
		return nil
	}

	m := gomail.NewMessage()
	m.SetHeader("From", fmt.Sprintf("osmi <%s>", s.from))
	m.SetHeader("To", toEmail)
	m.SetHeader("Subject", fmt.Sprintf("Tu boleto para %s - osmi", eventName))

	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html lang="es">
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Tu boleto - osmi</title>
	</head>
	<body style="margin:0;padding:0;background-color:#05010f;font-family:Arial,Helvetica,sans-serif;">
		<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#05010f;padding:40px 20px;">
			<tr>
				<td align="center">
					<table width="100%%" cellpadding="0" cellspacing="0" style="max-width:600px;">
						<tr>
							<td style="padding-bottom:30px;text-align:center;">
								<span style="font-size:36px;font-weight:900;background:linear-gradient(to right,#ff2bd6,#7b61ff);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text;">osmi</span>
								<p style="color:#6f6a7d;font-size:12px;margin-top:4px;text-transform:uppercase;letter-spacing:3px;">momentos inolvidables</p>
							</td>
						</tr>
						<tr>
							<td style="background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);border-radius:24px;padding:40px;">
								<table width="100%%" cellpadding="0" cellspacing="0">
									<tr>
										<td style="padding-bottom:24px;">
											<span style="display:inline-block;background:rgba(34,197,94,0.15);border:1px solid rgba(34,197,94,0.2);color:#22c55e;font-size:11px;font-weight:700;padding:8px 16px;border-radius:99px;text-transform:uppercase;letter-spacing:2px;">Compra Confirmada</span>
										</td>
									</tr>
								</table>
								<h1 style="color:#f0edf6;font-size:28px;font-weight:900;margin:0 0 8px 0;">Gracias, %s!</h1>
								<p style="color:#a09ab5;font-size:16px;margin:0 0 32px 0;line-height:1.5;">Tu boleto para <strong style="color:#f0edf6;">%s</strong> esta listo.</p>
								<table width="100%%" cellpadding="0" cellspacing="0" style="background:rgba(255,255,255,0.02);border:1px solid rgba(255,255,255,0.04);border-radius:16px;padding:24px;margin-bottom:24px;">
									<tr><td style="padding-bottom:16px;color:#6f6a7d;font-size:11px;text-transform:uppercase;letter-spacing:2px;">Detalles del Evento</td></tr>
									<tr><td style="padding-bottom:12px;color:#f0edf6;font-size:14px;font-weight:600;">%s</td></tr>
									<tr><td style="color:#f0edf6;font-size:14px;font-weight:600;">%s</td></tr>
								</table>
								<table width="100%%" cellpadding="0" cellspacing="0" style="background:rgba(255,255,255,0.02);border:1px solid rgba(255,255,255,0.04);border-radius:16px;padding:24px;margin-bottom:24px;text-align:center;">
									<tr><td style="padding-bottom:16px;color:#6f6a7d;font-size:11px;text-transform:uppercase;letter-spacing:2px;">Tu Boleto</td></tr>
									<tr><td style="padding-bottom:16px;"><img src="data:image/png;base64,%s" alt="QR" width="160" height="160" style="border-radius:12px;background:white;padding:12px;" /></td></tr>
									<tr><td><span style="display:inline-block;background:rgba(255,43,214,0.1);border:1px solid rgba(255,43,214,0.15);color:#ff2bd6;font-size:18px;font-weight:900;padding:12px 32px;border-radius:12px;letter-spacing:3px;">%s</span></td></tr>
								</table>
								<p style="color:#6f6a7d;font-size:12px;text-align:center;margin:0;">Presenta este codigo QR en la entrada del evento.</p>
								<p style="color:#6f6a7d;font-size:11px;text-align:center;margin:8px 0 0 0;">osmi &copy; 2026 &middot; momentos inolvidables</p>
							</td>
						</tr>
					</table>
				</td>
			</tr>
		</table>
	</body>
	</html>
	`, customerName, eventName, eventDate, eventLocation, qrBase64, ticketCode)

	m.SetBody("text/html", html)

	d := gomail.NewDialer(s.host, s.port, s.username, s.password)
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}
